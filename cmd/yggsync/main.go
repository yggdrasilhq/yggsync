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

const version = "0.2.1"

func main() {
	cfgPath := flag.String("config", defaultConfigPath(), "Path to ygg_sync TOML config")
	jobList := flag.String("jobs", "", "Comma-separated list of job names to run (default: all)")
	list := flag.Bool("list", false, "List jobs and exit")
	dryRun := flag.Bool("dry-run", false, "Do not modify anything; pass --dry-run to rclone and retention")
	forceResync := flag.Bool("resync", false, "Pass --resync to bisync jobs in this run")
	forceBisync := flag.Bool("force-bisync", false, "Pass --force to bisync jobs in this run")
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
	r := runner.New(cfg, *dryRun, *forceResync, *forceBisync, version)

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
