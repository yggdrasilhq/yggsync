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
)

const version = "0.3.0"

func main() {
	cfgPath := flag.String("config", defaultConfigPath(), "Path to ygg_sync TOML config")
	jobList := flag.String("jobs", "", "Comma-separated list of job names to run (default: all)")
	list := flag.Bool("list", false, "List jobs and exit")
	dryRun := flag.Bool("dry-run", false, "Do not modify anything")
	worktreeOp := flag.String("worktree-op", "sync", "Worktree action for worktree jobs: sync, update, or commit")
	allowMassDelete := flag.Bool("allow-mass-delete", false, "Permit deleting a large share of hub files in one run (off by default as a safety guard)")
	reason := flag.String("reason", "manual", "Why this run was triggered: 'manual' bypasses the device gate; anything else (e.g. 'scheduled') is gated")
	runtimePath := flag.String("runtime", "", "Optional device-runtime TOML providing the [gate] policy (battery/temperature)")
	_ = flag.Bool("resync", false, "Deprecated compatibility flag; native worktree sync no longer uses rclone bisync")
	_ = flag.Bool("force-bisync", false, "Deprecated compatibility flag; native worktree sync no longer uses rclone bisync")
	showVersion := flag.Bool("version", false, "Print version and exit")

	// Accept job names as leading positional args, e.g. `yggsync obsidian -config X`.
	// Go's flag package stops at the first non-flag token, so pull those leading
	// job names off before parsing the remaining flags.
	raw := os.Args[1:]
	var positional []string
	i := 0
	for i < len(raw) && !strings.HasPrefix(raw[i], "-") {
		positional = append(positional, raw[i])
		i++
	}
	if err := flag.CommandLine.Parse(raw[i:]); err != nil {
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

	// Device gate: scheduled runs may be skipped on low battery / high temp.
	// Manual runs (reason=manual) always proceed. A `-runtime` policy overrides
	// the main config's [gate].
	if *reason != "manual" {
		policy := cfg.Gate
		if rt, err := config.LoadRuntime(*runtimePath); err != nil {
			log.Fatalf("load runtime: %v", err)
		} else if rt.Enabled || *runtimePath != "" {
			policy = rt
		}
		if skip, why := gate.Check(policy); skip {
			log.Printf("skipped (reason=%s): %s", *reason, why)
			gate.Notify("yggsync skipped", why)
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
	for _, arg := range append(positional, flag.Args()...) {
		if trimmed := strings.TrimSpace(arg); trimmed != "" {
			names = append(names, trimmed)
		}
	}

	ctx := context.Background()
	r := runner.New(cfg, *dryRun, *worktreeOp, version)
	r.SetAllowMassDelete(*allowMassDelete)

	if len(names) == 0 {
		names = make([]string, 0, len(cfg.Jobs))
		for _, j := range cfg.Jobs {
			names = append(names, j.Name)
		}
	}

	summary := r.RunJobs(ctx, names)
	for name, err := range summary.Failed {
		log.Printf("job %s: %v", name, err)
	}
	log.Printf("summary ok=%d failed=%d duration=%s", len(summary.Succeeded), len(summary.Failed), summary.Duration.Round(0))
	if len(summary.Failed) > 0 {
		os.Exit(1)
	}
}

func defaultConfigPath() string {
	if cfg := os.Getenv("YGG_SYNC_CONFIG"); cfg != "" {
		return cfg
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.config/ygg_sync.toml"
	}
	return home + "/.config/ygg_sync.toml"
}
