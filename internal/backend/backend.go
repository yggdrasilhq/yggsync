package backend

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hirochachacha/go-smb2"

	"yggsync/internal/config"
)

type Entry struct {
	Path    string
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
	IsDir   bool
}

type FS interface {
	Close() error
	Walk(ctx context.Context, fn func(Entry) error) error
	OpenReader(ctx context.Context, rel string) (io.ReadCloser, error)
	WriteFile(ctx context.Context, rel string, src io.Reader, mode os.FileMode, modTime time.Time) error
	MkdirAll(ctx context.Context, rel string, perm os.FileMode) error
	Remove(ctx context.Context, rel string) error
	RemoveAll(ctx context.Context, rel string) error
	Stat(ctx context.Context, rel string) (Entry, error)
}

func Open(cfg config.Config, remote string) (FS, error) {
	if targetName, rel, ok := parseTargetRemote(cfg, remote); ok {
		target, _ := cfg.Target(targetName)
		switch target.Type {
		case "smb":
			return openSMB(target, rel)
		case "local":
			return &localFS{root: filepath.Join(config.ExpandPath(target.Path), filepath.FromSlash(rel))}, nil
		default:
			return nil, fmt.Errorf("unsupported target type %q", target.Type)
		}
	}
	return &localFS{root: config.ExpandPath(remote)}, nil
}

func parseTargetRemote(cfg config.Config, remote string) (string, string, bool) {
	idx := strings.Index(remote, ":")
	if idx <= 0 {
		return "", "", false
	}
	name := remote[:idx]
	rel := strings.TrimPrefix(remote[idx+1:], "/")
	if _, ok := cfg.Target(name); !ok {
		return "", "", false
	}
	return name, rel, true
}

type localFS struct {
	root string
}

func (l *localFS) Close() error { return nil }

func (l *localFS) Walk(ctx context.Context, fn func(Entry) error) error {
	err := filepath.WalkDir(l.root, func(full string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if full == l.root {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(l.root, full)
		if err != nil {
			return err
		}
		return fn(Entry{
			Path:    filepath.ToSlash(rel),
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime().UTC(),
			IsDir:   d.IsDir(),
		})
	})
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (l *localFS) OpenReader(_ context.Context, rel string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(l.root, filepath.FromSlash(rel)))
}

func (l *localFS) WriteFile(_ context.Context, rel string, src io.Reader, mode os.FileMode, modTime time.Time) error {
	full := filepath.Join(l.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(full, modTime, modTime)
}

func (l *localFS) MkdirAll(_ context.Context, rel string, perm os.FileMode) error {
	return os.MkdirAll(filepath.Join(l.root, filepath.FromSlash(rel)), perm)
}

func (l *localFS) Remove(_ context.Context, rel string) error {
	err := os.Remove(filepath.Join(l.root, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (l *localFS) RemoveAll(_ context.Context, rel string) error {
	return os.RemoveAll(filepath.Join(l.root, filepath.FromSlash(rel)))
}

func (l *localFS) Stat(_ context.Context, rel string) (Entry, error) {
	info, err := os.Stat(filepath.Join(l.root, filepath.FromSlash(rel)))
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		Path:    rel,
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime().UTC(),
		IsDir:   info.IsDir(),
	}, nil
}

type smbFS struct {
	session *smb2.Session
	share   *smb2.Share
	base    string
}

func openSMB(target config.Target, rel string) (FS, error) {
	addr := net.JoinHostPort(target.Host, fmt.Sprintf("%d", target.Port))
	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return nil, err
	}
	dialer := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     target.ResolvedUsername(),
			Password: target.ResolvedPassword(),
			Domain:   target.Domain,
		},
	}
	session, err := dialer.Dial(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	share, err := session.Mount(target.Share)
	if err != nil {
		_ = session.Logoff()
		return nil, err
	}
	base := cleanRemote(path.Join(target.BasePath, rel))
	return &smbFS{session: session, share: share, base: base}, nil
}

func (s *smbFS) Close() error {
	_ = s.share.Umount()
	return s.session.Logoff()
}

func (s *smbFS) Walk(ctx context.Context, fn func(Entry) error) error {
	return s.walkDir(ctx, ".", fn)
}

func (s *smbFS) walkDir(ctx context.Context, rel string, fn func(Entry) error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	infos, err := s.share.WithContext(ctx).ReadDir(s.full(rel))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name() < infos[j].Name() })
	for _, info := range infos {
		childRel := cleanRemote(path.Join(rel, info.Name()))
		entry := Entry{
			Path:    childRel,
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime().UTC(),
			IsDir:   info.IsDir(),
		}
		if err := fn(entry); err != nil {
			return err
		}
		if info.IsDir() {
			if err := s.walkDir(ctx, childRel, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *smbFS) OpenReader(ctx context.Context, rel string) (io.ReadCloser, error) {
	return s.share.WithContext(ctx).Open(s.full(rel))
}

func (s *smbFS) WriteFile(ctx context.Context, rel string, src io.Reader, mode os.FileMode, modTime time.Time) error {
	full := s.full(rel)
	parent := path.Dir(full)
	if parent != "." {
		if err := s.share.WithContext(ctx).MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	f, err := s.share.WithContext(ctx).OpenFile(full, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return s.share.WithContext(ctx).Chtimes(full, modTime, modTime)
}

func (s *smbFS) MkdirAll(ctx context.Context, rel string, perm os.FileMode) error {
	return s.share.WithContext(ctx).MkdirAll(s.full(rel), perm)
}

func (s *smbFS) Remove(ctx context.Context, rel string) error {
	err := s.share.WithContext(ctx).Remove(s.full(rel))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *smbFS) RemoveAll(ctx context.Context, rel string) error {
	err := s.share.WithContext(ctx).RemoveAll(s.full(rel))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *smbFS) Stat(ctx context.Context, rel string) (Entry, error) {
	info, err := s.share.WithContext(ctx).Stat(s.full(rel))
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		Path:    rel,
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime().UTC(),
		IsDir:   info.IsDir(),
	}, nil
}

func (s *smbFS) full(rel string) string {
	rel = cleanRemote(rel)
	if s.base == "" || s.base == "." {
		return rel
	}
	if rel == "." || rel == "" {
		return s.base
	}
	return cleanRemote(path.Join(s.base, rel))
}

func cleanRemote(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = path.Clean("/" + strings.TrimPrefix(rel, "/"))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return "."
	}
	return rel
}
