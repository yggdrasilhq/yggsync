package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type KeepLatestRule struct {
	Glob string `toml:"glob"`
	Keep int    `toml:"keep"`
}

type Target struct {
	Name        string `toml:"name"`
	Type        string `toml:"type"`
	Host        string `toml:"host"`
	Port        int    `toml:"port"`
	Share       string `toml:"share"`
	BasePath    string `toml:"base_path"`
	Path        string `toml:"path"`
	Username    string `toml:"username"`
	Password    string `toml:"password"`
	UsernameEnv string `toml:"username_env"`
	PasswordEnv string `toml:"password_env"`
	Domain      string `toml:"domain"`
}

type Job struct {
	Name               string           `toml:"name"`
	Description        string           `toml:"description"`
	Type               string           `toml:"type"`
	Local              string           `toml:"local"`
	Remote             string           `toml:"remote"`
	Direction          string           `toml:"direction"`
	Flags              []string         `toml:"flags"`
	Include            []string         `toml:"include"`
	Exclude            []string         `toml:"exclude"`
	FilterRules        []string         `toml:"filter_rules"`
	LocalRetentionDays int              `toml:"local_retention_days"`
	KeepLatest         []KeepLatestRule `toml:"keep_latest"`
	ResyncOnExit       []int            `toml:"resync_on_exit"`
	ResyncFlags        []string         `toml:"resync_flags"`
	TimeoutSeconds     int              `toml:"timeout_seconds"`
	StateFile          string           `toml:"state_file"`
}

type Config struct {
	LockFile         string   `toml:"lock_file"`
	WorktreeStateDir string   `toml:"worktree_state_dir"`
	DefaultFlags     []string `toml:"default_flags"`
	Targets          []Target `toml:"targets"`
	Jobs             []Job    `toml:"jobs"`

	// Deprecated compatibility fields from the rclone-backed era.
	RcloneBinary string `toml:"rclone_binary"`
	RcloneConfig string `toml:"rclone_config"`
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
	if c.LockFile == "" {
		c.LockFile = "~/.local/state/yggsync.lock"
	}
	if c.WorktreeStateDir == "" {
		c.WorktreeStateDir = "~/.local/state/yggsync/worktrees"
	}

	targetSeen := make(map[string]struct{})
	for i, t := range c.Targets {
		if t.Name == "" {
			return errors.New("target missing name")
		}
		if _, ok := targetSeen[t.Name]; ok {
			return fmt.Errorf("duplicate target name: %s", t.Name)
		}
		targetSeen[t.Name] = struct{}{}

		t.Type = strings.ToLower(strings.TrimSpace(t.Type))
		if t.Type == "" {
			t.Type = "smb"
		}
		switch t.Type {
		case "smb":
			if t.Host == "" {
				return fmt.Errorf("target %s missing host", t.Name)
			}
			if t.Share == "" {
				return fmt.Errorf("target %s missing share", t.Name)
			}
			if t.Port == 0 {
				t.Port = 445
			}
		case "local":
			if t.Path == "" {
				return fmt.Errorf("target %s missing path", t.Name)
			}
		default:
			return fmt.Errorf("target %s has unsupported type %q", t.Name, t.Type)
		}
		c.Targets[i] = t
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
		j.Type = strings.ToLower(strings.TrimSpace(j.Type))
		if j.Type == "bisync" {
			j.Type = "worktree"
		}
		if j.Direction == "" {
			j.Direction = "push"
		}
		if j.TimeoutSeconds < 0 {
			return fmt.Errorf("job %s has invalid timeout_seconds=%d", j.Name, j.TimeoutSeconds)
		}
		if j.Local == "" {
			return fmt.Errorf("job %s missing local path", j.Name)
		}
		if j.Remote == "" {
			return fmt.Errorf("job %s missing remote path", j.Name)
		}
		if len(j.FilterRules) > 0 && (len(j.Include) > 0 || len(j.Exclude) > 0) {
			return fmt.Errorf("job %s mixes filter_rules with include/exclude; pick one filter style", j.Name)
		}
		switch j.Type {
		case "worktree", "copy", "sync", "retained_copy":
		default:
			return fmt.Errorf("job %s has unsupported type %q", j.Name, j.Type)
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

func (c Config) Target(name string) (Target, bool) {
	for _, t := range c.Targets {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

func (t Target) ResolvedUsername() string {
	if t.Username != "" {
		return t.Username
	}
	if t.UsernameEnv != "" {
		return os.Getenv(t.UsernameEnv)
	}
	return ""
}

func (t Target) ResolvedPassword() string {
	if t.Password != "" {
		return t.Password
	}
	if t.PasswordEnv != "" {
		return os.Getenv(t.PasswordEnv)
	}
	return ""
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
