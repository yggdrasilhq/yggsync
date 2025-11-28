package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type KeepLatestRule struct {
	Glob string `toml:"glob"`
	Keep int    `toml:"keep"`
}

type Job struct {
	Name               string           `toml:"name"`
	Type               string           `toml:"type"`
	Local              string           `toml:"local"`
	Remote             string           `toml:"remote"`
	Direction          string           `toml:"direction"`
	Flags              []string         `toml:"flags"`
	Include            []string         `toml:"include"`
	Exclude            []string         `toml:"exclude"`
	LocalRetentionDays int              `toml:"local_retention_days"`
	KeepLatest         []KeepLatestRule `toml:"keep_latest"`
	ResyncOnExit       []int            `toml:"resync_on_exit"`
	ResyncFlags        []string         `toml:"resync_flags"`
}

type Config struct {
	RcloneBinary string   `toml:"rclone_binary"`
	RcloneConfig string   `toml:"rclone_config"`
	DefaultFlags []string `toml:"default_flags"`
	Jobs         []Job    `toml:"jobs"`
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(expandPath(path))
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.fillDefaults(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) fillDefaults() error {
	if c.RcloneBinary == "" {
		c.RcloneBinary = "rclone"
	}
	if c.RcloneConfig == "" {
		c.RcloneConfig = "~/.config/rclone/rclone.conf"
	}
	if len(c.DefaultFlags) == 0 {
		c.DefaultFlags = []string{"--fast-list", "--stats=30s", "--use-json-log"}
	}
	seen := make(map[string]struct{})
	for i, j := range c.Jobs {
		if j.Name == "" {
			return errors.New("job missing name")
		}
		if _, ok := seen[j.Name]; ok {
			return errors.New("duplicate job name: " + j.Name)
		}
		seen[j.Name] = struct{}{}
		j.Type = strings.ToLower(j.Type)
		if j.Direction == "" {
			j.Direction = "push"
		}
		c.Jobs[i] = j
	}
	return nil
}

func (c Config) Job(name string) (Job, bool) {
	for _, j := range c.Jobs {
		if j.Name == name {
			return j, true
		}
	}
	return Job{}, false
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func ExpandPath(p string) string {
	return expandPath(p)
}
