package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"yggsync/internal/config"
	"yggsync/internal/gate"
	"yggsync/internal/runner"
	"yggsync/internal/termuxjobs"
)

const version = "0.4.1"

func main() {
	// Subcommand-compatible entry: the Android wrappers invoke
	//   yggsync run -profile bulk …        (profile run)
	//   yggsync obsidian -reason scheduled (single job by name)
	//   yggsync android install-jobs …     (Termux job registration)
	// `run` is a marker, not a job; `android` and `policy` are real
	// subcommands. Everything else stays the plain job CLI.
	raw := os.Args[1:]
	var positional []string
	i := 0
	for i < len(raw) && !strings.HasPrefix(raw[i], "-") {
		positional = append(positional, raw[i])
		i++
	}
	rest := raw[i:]

	if len(positional) > 0 {
		switch positional[0] {
		case "run":
			positional = positional[1:]
		case "android":
			if err := termuxjobs.InstallJobs(version, rest); err != nil {
				log.Fatalf("android install-jobs: %v", err)
			}
			return
		case "policy":
			log.Fatalf("unsupported subcommand %q in this build (only run / android install-jobs)", positional[0])
		}
	}

	fs := flag.NewFlagSet("yggsync", flag.ContinueOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "Path to ygg_sync TOML config")
	jobList := fs.String("jobs", "", "Comma-separated list of job names to run (default: all)")
	list := fs.Bool("list", false, "List jobs and exit")
	dryRun := fs.Bool("dry-run", false, "Do not modify anything")
	worktreeOp := fs.String("worktree-op", "sync", "Worktree action for worktree jobs: sync, update, or commit")
	allowMassDelete := fs.Bool("allow-mass-delete", false, "Permit deleting a large share of hub files in one run (off by default as a safety guard)")
	reason := fs.String("reason", "manual", "Why this run was triggered: 'manual' bypasses the device gate; anything else (e.g. 'scheduled') is gated")
	runtimePath := fs.String("runtime", "", "Optional device-runtime TOML providing the [gate] policy or [profiles.<name>] entries")
	profileName := fs.String("profile", "", "Device profile from the runtime TOML used for gating and failure notifications")
	_ = fs.Bool("resync", false, "Deprecated compatibility flag; native worktree sync no longer uses rclone bisync")
	_ = fs.Bool("force-bisync", false, "Deprecated compatibility flag; native worktree sync no longer uses rclone bisync")
	showVersion := fs.Bool("version", false, "Print version and exit")
	if err := fs.Parse(rest); err != nil {
		os.Exit(2)
	}

	if *showVersion {
		fmt.Println("yggsync", version)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if *list {
		for _, j := range cfg.Jobs {
			fmt.Println(j.Name)
		}
		return
	}

	profiles, err := config.LoadProfiles(*runtimePath)
	if err != nil {
		log.Fatalf("load runtime: %v", err)
	}

	// Effective profile: -profile wins; otherwise a single named job whose
	// runtime profile exists (the `yggsync obsidian …` wrapper form).
	label := *profileName
	if label == "" && len(positional) == 1 {
		if _, ok := profiles[positional[0]]; ok {
			label = positional[0]
		}
	}
	var deviceProfile config.DeviceProfile
	if label != "" {
		dp, ok := profiles[label]
		if !ok {
			log.Printf("profile=%s not found in runtime TOML; running ungated", label)
		}
		deviceProfile = dp
	}

	// Device gate: scheduled runs may be skipped on low battery / high temp.
	// Manual runs (reason=manual) always proceed. A `-runtime` [gate] policy
	// applies when no named profile is in play.
	if *reason != "manual" {
		policy := deviceProfile.Gate()
		if label == "" {
			if rt, err := config.LoadRuntime(*runtimePath); err != nil {
				log.Fatalf("load runtime: %v", err)
			} else if rt.Enabled || *runtimePath != "" {
				policy = rt
			}
		}
		if skip, why := gate.Check(policy); skip {
			log.Printf("profile=%s reason=%s skipped: %s", label, *reason, why)
			// Routine scheduled skips stay log-only to keep the phone's
			// notification shade quiet; interactive runs get told.
			if *reason != "scheduled" {
				gate.Notify("yggsync skipped", why)
			}
			return
		}
	}

	names := []string{}
	if *jobList != "" {
		for _, part := range strings.Split(*jobList, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				names = append(names, trimmed)
			}
		}
	}
	// Leading positional args (before flags) and any trailing ones are job names.
	for _, arg := range append(positional, fs.Args()...) {
		if trimmed := strings.TrimSpace(arg); trimmed != "" {
			names = append(names, trimmed)
		}
	}

	// A profile run without an explicit job list covers the bulk media jobs;
	// worktree jobs keep their own dedicated scheduled wrappers.
	if len(names) == 0 && label != "" {
		for _, j := range cfg.Jobs {
			if j.Type != "worktree" {
				names = append(names, j.Name)
			}
		}
	}
	if len(names) == 0 {
		names = make([]string, 0, len(cfg.Jobs))
		for _, j := range cfg.Jobs {
			names = append(names, j.Name)
		}
	}

	ctx := context.Background()
	r := runner.New(cfg, *dryRun, *worktreeOp, version)
	r.SetAllowMassDelete(*allowMassDelete)

	summary := r.RunJobs(ctx, names)
	for name, err := range summary.Failed {
		log.Printf("job %s: %v", name, err)
	}
	log.Printf("summary ok=%d failed=%d duration=%s", len(summary.Succeeded), len(summary.Failed), summary.Duration.Round(0))
	if len(summary.Failed) > 0 {
		if deviceProfile.Notify {
			for _, name := range names {
				if err, ok := summary.Failed[name]; ok {
					gate.Notify(fmt.Sprintf("yggsync %s stopped", label), err.Error())
					break
				}
			}
		}
		os.Exit(1)
	}
}
