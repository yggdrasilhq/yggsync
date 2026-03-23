package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"yggsync/internal/config"
)

type Runner struct {
	cfg         config.Config
	dryRun      bool
	forceResync bool
	forceBisync bool
	version     string
}

type Summary struct {
	Succeeded []string
	Failed    map[string]error
	Duration  time.Duration
}

func New(cfg config.Config, dryRun bool, forceResync bool, forceBisync bool, version string) *Runner {
	return &Runner{
		cfg: cfg, dryRun: dryRun, forceResync: forceResync, forceBisync: forceBisync, version: version,
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
	case "bisync":
		return r.runBisync(jobCtx, job)
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

func (r *Runner) runBisync(ctx context.Context, job config.Job) error {
	args := r.buildCommonArgs("bisync", job)
	if r.forceResync {
		args = append(args, "--resync")
	}
	if r.forceBisync {
		args = append(args, "--force")
	}
	if err := r.execRclone(ctx, job, args); err != nil {
		return r.maybeResync(ctx, job, err)
	}
	return nil
}

func (r *Runner) runCopy(ctx context.Context, job config.Job) error {
	if job.Type == "retained_copy" {
		return r.runRetainedCopy(ctx, job)
	}
	local := config.ExpandPath(job.Local)
	if job.LocalRetentionDays > 0 {
		if err := applyLocalRetention(local, job.LocalRetentionDays, r.dryRun); err != nil {
			return fmt.Errorf("local retention: %w", err)
		}
	}
	if len(job.KeepLatest) > 0 {
		if err := applyKeepLatest(local, job.KeepLatest, r.dryRun); err != nil {
			return fmt.Errorf("keep_latest: %w", err)
		}
	}

	subcmd := "copy"
	if job.Type == "sync" {
		subcmd = "sync"
	}
	args := r.buildCommonArgs(subcmd, job)
	return r.execRclone(ctx, job, args)
}

func (r *Runner) runRetainedCopy(ctx context.Context, job config.Job) error {
	local := config.ExpandPath(job.Local)

	// Push first; only prune after we confirm the file exists on the remote.
	if err := r.execRclone(ctx, job, r.buildCommonArgs("copy", job)); err != nil {
		return err
	}

	var candidates []string
	if job.LocalRetentionDays > 0 {
		ret, err := retentionCandidates(local, job.LocalRetentionDays)
		if err != nil {
			return fmt.Errorf("local retention: %w", err)
		}
		candidates = append(candidates, ret...)
	}
	if len(job.KeepLatest) > 0 {
		kl, err := keepLatestCandidates(local, job.KeepLatest)
		if err != nil {
			return fmt.Errorf("keep_latest: %w", err)
		}
		candidates = append(candidates, kl...)
	}
	if len(candidates) == 0 {
		return nil
	}
	return r.safeDeleteIfRemoteExists(ctx, job, local, candidates)
}

func (r *Runner) buildCommonArgs(op string, job config.Job) []string {
	local := config.ExpandPath(job.Local)
	args := []string{op, local, job.Remote}
	args = append(args, r.cfg.DefaultFlags...)
	args = append(args, job.Flags...)
	if len(job.FilterRules) > 0 {
		for _, rule := range job.FilterRules {
			args = append(args, "--filter", rule)
		}
	} else {
		for _, inc := range job.Include {
			args = append(args, "--include", inc)
		}
		for _, exc := range job.Exclude {
			args = append(args, "--exclude", exc)
		}
	}
	if r.dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

func (r *Runner) execRclone(ctx context.Context, job config.Job, args []string) error {
	cmd := exec.CommandContext(ctx, r.cfg.RcloneBinary, args...)
	cmd.Env = append(os.Environ(), "RCLONE_CONFIG="+config.ExpandPath(r.cfg.RcloneConfig))
	out, err := cmd.CombinedOutput()
	log.Printf("job=%s op=%s\n%s", job.Name, args[0], strings.TrimSpace(string(out)))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%s: timed out after %ds", job.Name, job.TimeoutSeconds)
		}
		if exitErr := (&exec.ExitError{}); errors.As(err, &exitErr) {
			return &commandError{name: job.Name, code: exitErr.ExitCode(), output: string(out)}
		}
		return err
	}
	return nil
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

func (r *Runner) maybeResync(ctx context.Context, job config.Job, err error) error {
	var cmdErr *commandError
	if !errors.As(err, &cmdErr) {
		return err
	}
	for _, code := range job.ResyncOnExit {
		if code == cmdErr.code {
			log.Printf("job %s: retrying with --resync due to exit code %d", job.Name, code)
			args := r.buildCommonArgs("bisync", job)
			args = append(args, "--resync")
			args = append(args, job.ResyncFlags...)
			return r.execRclone(ctx, job, args)
		}
	}
	return err
}

type commandError struct {
	name   string
	code   int
	output string
}

func (e *commandError) Error() string {
	return fmt.Sprintf("%s: rclone exit %d", e.name, e.code)
}

// safeDeleteIfRemoteExists removes local files only when a matching remote file exists.
// Any remote check error leaves the local file intact.
func (r *Runner) safeDeleteIfRemoteExists(ctx context.Context, job config.Job, localRoot string, paths []string) error {
	seen := make(map[string]struct{})
	for _, p := range paths {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}

		has, err := r.remoteFileExists(ctx, job, localRoot, p)
		if err != nil {
			log.Printf("skip delete (remote check failed) %s: %v", p, err)
			continue
		}
		if !has {
			log.Printf("skip delete (not on remote) %s", p)
			continue
		}
		if r.dryRun {
			log.Printf("[dry-run] would delete %s (confirmed on remote)", p)
			continue
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		log.Printf("deleted %s (confirmed on remote)", p)
	}
	return nil
}

func (r *Runner) remoteFileExists(ctx context.Context, job config.Job, localRoot, localPath string) (bool, error) {
	localInfo, err := os.Stat(localPath)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(localRoot, localPath)
	if err != nil {
		return false, err
	}
	remotePath := path.Join(job.Remote, filepath.ToSlash(rel))
	args := []string{"lsjson", remotePath, "--files-only"}
	cmd := exec.CommandContext(ctx, r.cfg.RcloneBinary, args...)
	cmd.Env = append(os.Environ(), "RCLONE_CONFIG="+config.ExpandPath(r.cfg.RcloneConfig))
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// rclone uses exit code 3 when a path is not found.
			if exitErr.ExitCode() == 3 {
				return false, nil
			}
		}
		return false, err
	}
	var entries []struct {
		Size    int64  `json:"Size"`
		ModTime string `json:"ModTime"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return false, nil
	}
	info := entries[0]
	if info.Size < localInfo.Size() {
		log.Printf("skip delete (remote smaller) %s remote=%d local=%d", localPath, info.Size, localInfo.Size())
		return false, nil
	}
	if t, parseErr := time.Parse(time.RFC3339Nano, info.ModTime); parseErr == nil {
		// Allow small drift; require remote to be at least as new.
		if t.Before(localInfo.ModTime().Add(-2 * time.Second)) {
			log.Printf("skip delete (remote older) %s remote=%s local=%s", localPath, t, localInfo.ModTime())
			return false, nil
		}
	}
	return true, nil
}

func applyLocalRetention(root string, days int, dryRun bool) error {
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(cutoff) {
			if dryRun {
				log.Printf("[dry-run] would delete %s (age %s)", path, time.Since(info.ModTime()).Round(time.Minute))
				return nil
			}
			if remErr := os.Remove(path); remErr != nil {
				return remErr
			}
			log.Printf("deleted old file %s (age %s)", path, time.Since(info.ModTime()).Round(time.Minute))
		}
		return nil
	})
}

func applyKeepLatest(root string, rules []config.KeepLatestRule, dryRun bool) error {
	for _, rule := range rules {
		if rule.Keep < 1 {
			continue
		}
		matches := make([]string, 0)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if ok, _ := filepath.Match(rule.Glob, rel); ok {
				matches = append(matches, path)
			}
			return nil
		})
		if err != nil {
			return err
		}
		sort.Slice(matches, func(i, j int) bool {
			ai, _ := os.Stat(matches[i])
			aj, _ := os.Stat(matches[j])
			return ai.ModTime().After(aj.ModTime())
		})
		for idx, path := range matches {
			if idx < rule.Keep {
				continue
			}
			if dryRun {
				log.Printf("[dry-run] would delete %s (keep_latest rule %s)", path, rule.Glob)
				continue
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			log.Printf("deleted %s (keep_latest rule %s)", path, rule.Glob)
		}
	}
	return nil
}

func retentionCandidates(root string, days int) ([]string, error) {
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	matches := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(cutoff) {
			matches = append(matches, path)
		}
		return nil
	})
	return matches, err
}

func keepLatestCandidates(root string, rules []config.KeepLatestRule) ([]string, error) {
	toDelete := make([]string, 0)
	for _, rule := range rules {
		if rule.Keep < 1 {
			continue
		}
		matches := make([]string, 0)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if ok, _ := filepath.Match(rule.Glob, rel); ok {
				matches = append(matches, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Slice(matches, func(i, j int) bool {
			ai, _ := os.Stat(matches[i])
			aj, _ := os.Stat(matches[j])
			return ai.ModTime().After(aj.ModTime())
		})
		for idx, path := range matches {
			if idx >= rule.Keep {
				toDelete = append(toDelete, path)
			}
		}
	}
	return toDelete, nil
}
