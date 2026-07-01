package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"yggsync/internal/backend"
	"yggsync/internal/config"
	"yggsync/internal/filter"
)

type Runner struct {
	cfg             config.Config
	dryRun          bool
	worktreeOp      string
	version         string
	allowMassDelete bool
}

// SetAllowMassDelete permits a run to delete a large share of hub files in one
// pass. Off by default so an emptied or misconfigured local tree fails loudly
// instead of wiping the hub.
func (r *Runner) SetAllowMassDelete(v bool) { r.allowMassDelete = v }

type Summary struct {
	Succeeded []string
	Failed    map[string]error
	Duration  time.Duration
}

type Snapshot struct {
	Files map[string]FileState `json:"files"`
	Dirs  map[string]DirState  `json:"dirs"`
}

type FileState struct {
	Size    int64     `json:"size"`
	Mode    uint32    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
	Hash    string    `json:"hash,omitempty"`
}

type DirState struct {
	Mode uint32 `json:"mode"`
}

func New(cfg config.Config, dryRun bool, worktreeOp string, version string) *Runner {
	if worktreeOp == "" {
		worktreeOp = "sync"
	}
	return &Runner{
		cfg: cfg, dryRun: dryRun, worktreeOp: strings.ToLower(worktreeOp), version: version,
	}
}

func (r *Runner) RunJob(ctx context.Context, name string) error {
	job, ok := r.cfg.Job(name)
	if !ok {
		return fmt.Errorf("unknown job %q", name)
	}
	jobCtx := ctx
	var cancel context.CancelFunc
	if job.TimeoutSeconds > 0 {
		jobCtx, cancel = context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	switch job.Type {
	case "worktree":
		return r.runWorktree(jobCtx, job)
	case "copy", "sync":
		return r.runCopy(jobCtx, job)
	case "retained_copy":
		return r.runRetainedCopy(jobCtx, job)
	default:
		return fmt.Errorf("job %s: unsupported type %q", job.Name, job.Type)
	}
}

func (r *Runner) RunJobs(ctx context.Context, names []string) Summary {
	start := time.Now()
	summary := Summary{
		Succeeded: make([]string, 0, len(names)),
		Failed:    make(map[string]error),
	}
	unlock, err := acquireLock(config.ExpandPath(r.cfg.LockFile))
	if err != nil {
		summary.Failed["_lock"] = err
		summary.Duration = time.Since(start)
		return summary
	}
	defer unlock()

	for _, name := range names {
		if err := r.RunJob(ctx, name); err != nil {
			summary.Failed[name] = err
			log.Printf("job %s failed: %v", name, err)
			continue
		}
		summary.Succeeded = append(summary.Succeeded, name)
	}
	summary.Duration = time.Since(start)
	return summary
}

func (r *Runner) runCopy(ctx context.Context, job config.Job) error {
	local, remote, matcher, err := r.openJobFS(ctx, job)
	if err != nil {
		return err
	}
	defer local.Close()
	defer remote.Close()

	localSnap, err := scanFS(ctx, local, matcher, false)
	if err != nil {
		return err
	}
	remoteSnap, err := scanFS(ctx, remote, matcher, false)
	if err != nil {
		return err
	}

	if err := createDirs(ctx, remote, localSnap, r.dryRun); err != nil {
		return err
	}
	if err := copyMissingOrChanged(ctx, local, remote, localSnap, remoteSnap, r.dryRun); err != nil {
		return err
	}

	if job.Type == "sync" {
		if err := deleteMissingRemote(ctx, remote, localSnap, remoteSnap, r.dryRun); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) runRetainedCopy(ctx context.Context, job config.Job) error {
	local, remote, matcher, err := r.openJobFS(ctx, job)
	if err != nil {
		return err
	}
	defer local.Close()
	defer remote.Close()

	localSnap, err := scanFS(ctx, local, matcher, false)
	if err != nil {
		return err
	}
	remoteSnap, err := scanFS(ctx, remote, matcher, false)
	if err != nil {
		return err
	}

	if err := createDirs(ctx, remote, localSnap, r.dryRun); err != nil {
		return err
	}
	if err := copyMissingOrChanged(ctx, local, remote, localSnap, remoteSnap, r.dryRun); err != nil {
		return err
	}

	var candidates []string
	if job.LocalRetentionDays > 0 {
		ret, err := retentionCandidates(localSnap, job.LocalRetentionDays)
		if err != nil {
			return fmt.Errorf("local retention: %w", err)
		}
		candidates = append(candidates, ret...)
	}
	if len(job.KeepLatest) > 0 {
		kl, err := keepLatestCandidates(localSnap, job.KeepLatest)
		if err != nil {
			return fmt.Errorf("keep_latest: %w", err)
		}
		candidates = append(candidates, kl...)
	}
	if len(candidates) == 0 {
		return nil
	}
	return safeDeleteIfRemoteExists(ctx, local, remote, localSnap, candidates, r.dryRun)
}

func (r *Runner) openJobFS(ctx context.Context, job config.Job) (backend.FS, backend.FS, *filter.Matcher, error) {
	local, err := backend.Open(r.cfg, config.ExpandPath(job.Local))
	if err != nil {
		return nil, nil, nil, err
	}
	remote, err := backend.Open(r.cfg, job.Remote)
	if err != nil {
		_ = local.Close()
		return nil, nil, nil, err
	}
	matcher, err := filter.New(job)
	if err != nil {
		_ = local.Close()
		_ = remote.Close()
		return nil, nil, nil, err
	}
	_ = ctx
	return local, remote, matcher, nil
}

func scanFS(ctx context.Context, fs backend.FS, matcher *filter.Matcher, withHash bool) (Snapshot, error) {
	snap := Snapshot{
		Files: make(map[string]FileState),
		Dirs:  make(map[string]DirState),
	}
	err := fs.Walk(ctx, func(entry backend.Entry) error {
		rel := filepath.ToSlash(entry.Path)
		if !matcher.Match(rel) {
			return nil
		}
		if entry.IsDir {
			snap.Dirs[rel] = DirState{Mode: uint32(entry.Mode.Perm())}
			return nil
		}
		state := FileState{
			Size:    entry.Size,
			Mode:    uint32(entry.Mode.Perm()),
			ModTime: entry.ModTime.UTC(),
		}
		if withHash {
			sum, err := hashFile(ctx, fs, rel)
			if err != nil {
				return err
			}
			state.Hash = sum
		}
		snap.Files[rel] = state
		return nil
	})
	return snap, err
}

func hashFile(ctx context.Context, fs backend.FS, rel string) (string, error) {
	r, err := fs.OpenReader(ctx, rel)
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func createDirs(ctx context.Context, dst backend.FS, snap Snapshot, dryRun bool) error {
	dirs := make([]string, 0, len(snap.Dirs))
	for dir := range snap.Dirs {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if dryRun {
			log.Printf("[dry-run] would create dir %s", dir)
			continue
		}
		if err := dst.MkdirAll(ctx, dir, os.FileMode(snap.Dirs[dir].Mode)); err != nil {
			return err
		}
	}
	return nil
}

func copyMissingOrChanged(ctx context.Context, src, dst backend.FS, srcSnap, dstSnap Snapshot, dryRun bool) error {
	paths := make([]string, 0, len(srcSnap.Files))
	for p := range srcSnap.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		srcState := srcSnap.Files[rel]
		dstState, ok := dstSnap.Files[rel]
		if ok && sameFile(srcState, dstState) {
			continue
		}
		if dryRun {
			log.Printf("[dry-run] would copy %s", rel)
			continue
		}
		if err := copyFile(ctx, src, dst, rel, srcState); err != nil {
			return err
		}
	}
	return nil
}

func deleteMissingRemote(ctx context.Context, remote backend.FS, localSnap, remoteSnap Snapshot, dryRun bool) error {
	paths := make([]string, 0)
	for rel := range remoteSnap.Files {
		if _, ok := localSnap.Files[rel]; !ok {
			paths = append(paths, rel)
		}
	}
	sort.Strings(paths)
	for _, rel := range paths {
		if dryRun {
			log.Printf("[dry-run] would delete remote %s", rel)
			continue
		}
		if err := remote.Remove(ctx, rel); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(ctx context.Context, src, dst backend.FS, rel string, state FileState) error {
	r, err := src.OpenReader(ctx, rel)
	if err != nil {
		return err
	}
	defer r.Close()
	return dst.WriteFile(ctx, rel, r, os.FileMode(state.Mode), state.ModTime)
}

func safeDeleteIfRemoteExists(ctx context.Context, local, remote backend.FS, localSnap Snapshot, paths []string, dryRun bool) error {
	seen := make(map[string]struct{})
	for _, rel := range paths {
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}

		localState, ok := localSnap.Files[rel]
		if !ok {
			continue
		}
		remoteState, err := statFile(ctx, remote, rel)
		if err != nil {
			log.Printf("skip delete (remote check failed) %s: %v", rel, err)
			continue
		}
		if !sameFile(localState, remoteState) {
			log.Printf("skip delete (remote differs) %s", rel)
			continue
		}
		if dryRun {
			log.Printf("[dry-run] would delete %s (confirmed on remote)", rel)
			continue
		}
		if err := local.Remove(ctx, rel); err != nil {
			return err
		}
		log.Printf("deleted %s (confirmed on remote)", rel)
	}
	return nil
}

func statFile(ctx context.Context, fs backend.FS, rel string) (FileState, error) {
	entry, err := fs.Stat(ctx, rel)
	if err != nil {
		return FileState{}, err
	}
	return FileState{
		Size:    entry.Size,
		Mode:    uint32(entry.Mode.Perm()),
		ModTime: entry.ModTime.UTC(),
	}, nil
}

func sameFile(a, b FileState) bool {
	if a.Size != b.Size {
		return false
	}
	if !sameTime(a.ModTime, b.ModTime) {
		return false
	}
	if a.Hash != "" && b.Hash != "" && a.Hash != b.Hash {
		return false
	}
	return true
}

func sameTime(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= 2*time.Second
}

func retentionCandidates(snap Snapshot, days int) ([]string, error) {
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	matches := make([]string, 0)
	for rel, state := range snap.Files {
		if state.ModTime.Before(cutoff) {
			matches = append(matches, rel)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func keepLatestCandidates(snap Snapshot, rules []config.KeepLatestRule) ([]string, error) {
	toDelete := make([]string, 0)
	for _, rule := range rules {
		if rule.Keep < 1 {
			continue
		}
		matches := make([]string, 0)
		for rel := range snap.Files {
			if ok, _ := filepath.Match(rule.Glob, rel); ok {
				matches = append(matches, rel)
			}
		}
		sort.Slice(matches, func(i, j int) bool {
			return snap.Files[matches[i]].ModTime.After(snap.Files[matches[j]].ModTime)
		})
		for idx, rel := range matches {
			if idx >= rule.Keep {
				toDelete = append(toDelete, rel)
			}
		}
	}
	sort.Strings(toDelete)
	return toDelete, nil
}

func conflictError(jobName string, conflicts []string) error {
	if len(conflicts) > 8 {
		return fmt.Errorf("job %s: worktree conflicts on %d paths, first few: %s", jobName, len(conflicts), strings.Join(conflicts[:8], ", "))
	}
	return fmt.Errorf("job %s: worktree conflicts: %s", jobName, strings.Join(conflicts, ", "))
}

func acquireLock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another yggsync run is already active (%s)", path)
		}
		return nil, err
	}
	if err := file.Truncate(0); err == nil {
		_, _ = file.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
