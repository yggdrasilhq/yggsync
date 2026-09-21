// Package termuxjobs regenerates the per-profile wrapper scripts under
// ~/.local/state/yggsync/jobs/ and re-registers them with Termux's job
// scheduler (`android install-jobs`). Termux:Boot re-runs it at every
// power-on, so persisted job definitions survive reboots and package
// upgrades that wipe scheduler state.
package termuxjobs

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"yggsync/internal/config"
)

const (
	obsidianJobID = 101
	bulkJobID     = 102
)

func InstallJobs(version string, args []string) error {
	fs := flag.NewFlagSet("android install-jobs", flag.ContinueOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "Path to ygg_sync TOML config")
	runtimePath := fs.String("runtime", "", "Device-runtime TOML (passed through to the wrappers)")
	obsidianPeriod := fs.Int64("obsidian-period-ms", 3*60*60*1000, "Period for the worktree/obsidian job in milliseconds")
	bulkPeriod := fs.Int64("bulk-period-ms", 12*60*60*1000, "Period for the bulk media job in milliseconds")
	showVersion := fs.Bool("version", false, "Print version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("yggsync", version)
		return nil
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}

	stateDir := config.ExpandPath("~/.local/state/yggsync")
	jobsDir := filepath.Join(stateDir, "jobs")
	if err := os.MkdirAll(jobsDir, 0o755); err != nil {
		return err
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("bash not found: %w", err)
	}

	var worktreeName string
	for _, j := range cfg.Jobs {
		if j.Type == "worktree" && worktreeName == "" {
			worktreeName = j.Name
		}
	}

	if worktreeName != "" {
		script := filepath.Join(jobsDir, "run-obsidian.sh")
		body := fmt.Sprintf("#!%s\nexec '%s' %s -config '%s' -runtime '%s' -reason scheduled >>'%s' 2>&1\n",
			bash, self, worktreeName, *cfgPath, *runtimePath,
			filepath.Join(stateDir, "obsidian.log"))
		if err := writeScript(script, body); err != nil {
			return err
		}
		if err := register(obsidianJobID, script, *obsidianPeriod, false); err != nil {
			return err
		}
	}

	script := filepath.Join(jobsDir, "run-bulk.sh")
	body := fmt.Sprintf("#!%s\nexec '%s' run -config '%s' -runtime '%s' -profile bulk -reason scheduled >>'%s' 2>&1\n",
		bash, self, *cfgPath, *runtimePath,
		filepath.Join(stateDir, "bulk.log"))
	if err := writeScript(script, body); err != nil {
		return err
	}
	return register(bulkJobID, script, *bulkPeriod, true)
}

func writeScript(path, body string) error {
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// register hands one wrapper to termux-job-scheduler. Unmetered network keeps
// scheduled runs off cellular data; the binary-side gate additionally skips
// low-battery / hot-device runs. A missing scheduler binary (non-Termux host,
// Termux:API app absent) is reported but not fatal — the wrappers still exist
// and can be run by hand.
func register(jobID int, script string, periodMs int64, batteryNotLow bool) error {
	sched, err := exec.LookPath("termux-job-scheduler")
	if err != nil {
		fmt.Printf("termux-job-scheduler not found; wrote %s but did not register job %d\n", script, jobID)
		return nil
	}
	args := []string{
		"--job-id", fmt.Sprintf("%d", jobID),
		"--script", script,
		"--period-ms", fmt.Sprintf("%d", periodMs),
		"--network", "unmetered",
		"--persisted", "true",
	}
	if batteryNotLow {
		args = append(args, "--battery-not-low", "true")
	}
	cmd := exec.Command(sched, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
