package runner

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"yggsync/internal/config"
)

type Runner struct {
	cfg     config.Config
	dryRun  bool
	version string
}

func New(cfg config.Config, dryRun bool, version string) *Runner {
	return &Runner{cfg: cfg, dryRun: dryRun, version: version}
}

func (r *Runner) RunJob(ctx context.Context, name string) error {
	job, ok := r.cfg.Job(name)
	if !ok {
		return fmt.Errorf("unknown job %q", name)
	}

	switch job.Type {
	case "bisync":
		return r.runBisync(ctx, job)
	case "copy", "sync", "retained_copy":
		return r.runCopy(ctx, job)
	default:
		return fmt.Errorf("job %s: unsupported type %q", job.Name, job.Type)
	}
}

func (r *Runner) runBisync(ctx context.Context, job config.Job) error {
	args := r.buildCommonArgs("bisync", job)
	if err := r.execRclone(ctx, job, args); err != nil {
		return r.maybeResync(ctx, job, err)
	}
	return nil
}

func (r *Runner) runCopy(ctx context.Context, job config.Job) error {
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

func (r *Runner) buildCommonArgs(op string, job config.Job) []string {
	local := config.ExpandPath(job.Local)
	args := []string{op, local, job.Remote}
	args = append(args, r.cfg.DefaultFlags...)
	args = append(args, job.Flags...)
	for _, inc := range job.Include {
		args = append(args, "--include", inc)
	}
	for _, exc := range job.Exclude {
		args = append(args, "--exclude", exc)
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
		if exitErr := (&exec.ExitError{}); errors.As(err, &exitErr) {
			return &commandError{name: job.Name, code: exitErr.ExitCode(), output: string(out)}
		}
		return err
	}
	return nil
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
