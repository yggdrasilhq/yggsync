package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"yggsync/internal/gate"
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
	ClientID           string           `toml:"client_id"` // identity in the hub ledger's client cursors
	NoMerge            bool             `toml:"no_merge"`  // disable diff3; divergence goes straight to .mergefail
}

type Config struct {
	LockFile         string      `toml:"lock_file"`
	WorktreeStateDir string      `toml:"worktree_state_dir"`
	DefaultFlags     []string    `toml:"default_flags"`
	Gate             gate.Policy `toml:"gate"`
	Targets          []Target    `toml:"targets"`
	Jobs             []Job       `toml:"jobs"`

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

// LoadRuntime reads a small device-runtime TOML that carries a [gate] section,
// kept separate from the jobs config so device policy can vary per host. A
// missing path yields a zero (disabled) policy without error.
func LoadRuntime(path string) (gate.Policy, error) {
	if path == "" {
		return gate.Policy{}, nil
	}
	raw, err := os.ReadFile(expandPath(path))
	if err != nil {
		if os.IsNotExist(err) {
			return gate.Policy{}, nil
		}
		return gate.Policy{}, err
	}
	var rt struct {
		Gate gate.Policy `toml:"gate"`
	}
	if err := toml.Unmarshal(raw, &rt); err != nil {
		return gate.Policy{}, err
	}
	return rt.Gate, nil
}

// DeviceProfile is one [profiles.<name>] entry of the device-runtime TOML.
// It carries the same gating knobs under the names the Android wrappers use
// (min_battery, max_battery_temp_c) plus notification policy. Fields that
// only make sense to a long-lived scheduler on the phone (battery temperature
// sampling intervals, cellular byte caps, Wi-Fi weekday windows) are accepted
// by the TOML but not enforced here — scheduled jobs are already constrained
// to unmetered networks by the Termux job scheduler.
type DeviceProfile struct {
	Enabled            bool    `toml:"enabled"`
	MinBattery         int     `toml:"min_battery"`
	MaxBatteryTempC    float64 `toml:"max_battery_temp_c"`
	MonitorIntervalSec int     `toml:"monitor_interval_seconds"`
	Notify             bool    `toml:"notify"`
	CellularLimitBytes int64   `toml:"cellular_limit_bytes"`
}

// Gate renders the profile as a gate.Policy for the pre-run device gate.
func (p DeviceProfile) Gate() gate.Policy {
	return gate.Policy{
		Enabled:           p.Enabled || p.MinBattery > 0 || p.MaxBatteryTempC > 0,
		BatteryMinPercent: p.MinBattery,
		MaxBatteryTempC:   p.MaxBatteryTempC,
	}
}

// LoadProfiles reads the [profiles] table of the device-runtime TOML. A
// missing path yields an empty table without error.
func LoadProfiles(path string) (map[string]DeviceProfile, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(expandPath(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rt struct {
		Profiles map[string]DeviceProfile `toml:"profiles"`
	}
	if err := toml.Unmarshal(raw, &rt); err != nil {
		return nil, err
	}
	return rt.Profiles, nil
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

// DefaultConfigPath is the config path used when no -config flag is given.
func DefaultConfigPath() string {
	if cfg := os.Getenv("YGG_SYNC_CONFIG"); cfg != "" {
		return cfg
	}
	return "~/.config/ygg_sync.toml"
}
