// Package gate implements an optional pre-run device gate for scheduled syncs:
// skip (and notify) when the battery is low or the device is hot. It reads
// battery state via Termux's `termux-battery-status` and is a no-op wherever
// that command is unavailable, so the generic engine stays portable.
package gate

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// Policy describes when a scheduled run should be skipped. Zero-value fields
// fall back to built-in defaults via WithDefaults when Enabled is true.
type Policy struct {
	Enabled           bool    `toml:"enabled"`
	BatteryMinPercent int     `toml:"battery_min_percent"`
	RequireCharging   bool    `toml:"require_charging"`
	MaxBatteryTempC   float64 `toml:"max_battery_temp_c"`
}

// WithDefaults fills sane thresholds for any unset field.
func (p Policy) WithDefaults() Policy {
	if p.BatteryMinPercent == 0 {
		p.BatteryMinPercent = 20
	}
	if p.MaxBatteryTempC == 0 {
		p.MaxBatteryTempC = 45
	}
	return p
}

// Status is a snapshot of device power state.
type Status struct {
	Percentage int
	TempC      float64
	Charging   bool
}

// Decide is the pure gating decision, separated from I/O for testing.
func Decide(p Policy, s Status) (skip bool, reason string) {
	if !p.Enabled {
		return false, ""
	}
	p = p.WithDefaults()
	if p.MaxBatteryTempC > 0 && s.TempC >= p.MaxBatteryTempC {
		return true, fmt.Sprintf("device hot (%.0f°C ≥ %.0f°C)", s.TempC, p.MaxBatteryTempC)
	}
	if p.RequireCharging && !s.Charging {
		return true, "not charging (require_charging)"
	}
	if p.BatteryMinPercent > 0 && !s.Charging && s.Percentage < p.BatteryMinPercent {
		return true, fmt.Sprintf("battery low (%d%% < %d%%, not charging)", s.Percentage, p.BatteryMinPercent)
	}
	return false, ""
}

// Check reads device state and applies the policy. If the device state cannot
// be read (non-Termux host, missing command), it does not skip.
func Check(p Policy) (skip bool, reason string) {
	if !p.Enabled {
		return false, ""
	}
	s, ok := readBattery()
	if !ok {
		return false, ""
	}
	return Decide(p, s)
}

func readBattery() (Status, bool) {
	bin, err := exec.LookPath("termux-battery-status")
	if err != nil {
		return Status{}, false
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		return Status{}, false
	}
	var raw struct {
		Percentage  int     `json:"percentage"`
		Temperature float64 `json:"temperature"`
		Plugged     string  `json:"plugged"`
		Status      string  `json:"status"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return Status{}, false
	}
	charging := raw.Status == "CHARGING" || raw.Status == "FULL" ||
		(raw.Plugged != "" && raw.Plugged != "UNPLUGGED")
	return Status{Percentage: raw.Percentage, TempC: raw.Temperature, Charging: charging}, true
}

// Notify surfaces a skip reason as a Termux notification; a no-op elsewhere.
func Notify(title, msg string) {
	bin, err := exec.LookPath("termux-notification")
	if err != nil {
		return
	}
	_ = exec.Command(bin, "--title", title, "--content", msg, "--id", "yggsync-gate").Run()
}
