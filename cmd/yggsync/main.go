package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"yggsync/internal/config"
	"yggsync/internal/runner"
)

const version = "0.1.3"

func main() {
	cfgPath := flag.String("config", defaultConfigPath(), "Path to ygg_sync TOML config")
	jobList := flag.String("jobs", "", "Comma-separated list of job names to run (default: all)")
	list := flag.Bool("list", false, "List jobs and exit")
	dryRun := flag.Bool("dry-run", false, "Do not modify anything; pass --dry-run to rclone and retention")
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
	r := runner.New(cfg, *dryRun, version)

	if len(names) == 0 {
		names = make([]string, 0, len(cfg.Jobs))
		for _, j := range cfg.Jobs {
			names = append(names, j.Name)
		}
	}

	start := time.Now()
	for _, name := range names {
		if err := r.RunJob(ctx, name); err != nil {
			log.Printf("job %s: %v", name, err)
		}
	}
	log.Printf("done in %s", time.Since(start).Round(time.Millisecond))
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
