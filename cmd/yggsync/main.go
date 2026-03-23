package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"yggsync/internal/config"
	"yggsync/internal/runner"
)

const version = "0.3.0"

func main() {
	cfgPath := flag.String("config", defaultConfigPath(), "Path to ygg_sync TOML config")
	jobList := flag.String("jobs", "", "Comma-separated list of job names to run (default: all)")
	list := flag.Bool("list", false, "List jobs and exit")
	dryRun := flag.Bool("dry-run", false, "Do not modify anything")
	worktreeOp := flag.String("worktree-op", "sync", "Worktree action for worktree jobs: sync, update, or commit")
	_ = flag.Bool("resync", false, "Deprecated compatibility flag; native worktree sync no longer uses rclone bisync")
	_ = flag.Bool("force-bisync", false, "Deprecated compatibility flag; native worktree sync no longer uses rclone bisync")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

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

	names := []string{}
	if *jobList != "" {
		for _, part := range strings.Split(*jobList, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				names = append(names, trimmed)
			}
		}
	}

	ctx := context.Background()
	r := runner.New(cfg, *dryRun, *worktreeOp, version)

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
