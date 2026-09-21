package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The phone-side runtime TOML speaks [profiles.<name>] with min_battery /
// max_battery_temp_c; those must map onto the gate policy the wrappers rely
// on for scheduled-run skipping.
func TestLoadProfilesMapsGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yggsync.runtime.toml")
	body := `
[profiles.bulk]
min_battery = 65
max_battery_temp_c = 38.5
monitor_interval_seconds = 20
notify = true
cellular_limit_bytes = 524288000

[profiles.bulk.scheduled]
enabled = true
network = 'wifi'
allowed_weekdays = [0, 1, 2, 3, 4, 5, 6]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	profiles, err := LoadProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	bulk, ok := profiles["bulk"]
	if !ok {
		t.Fatal("bulk profile missing")
	}
	if bulk.MinBattery != 65 || bulk.MaxBatteryTempC != 38.5 || !bulk.Notify {
		t.Fatalf("unexpected profile: %+v", bulk)
	}
	gate := bulk.Gate()
	if !gate.Enabled || gate.BatteryMinPercent != 65 || gate.MaxBatteryTempC != 38.5 {
		t.Fatalf("gate mapping wrong: %+v", gate)
	}
}

func TestLoadProfilesMissingFileIsEmpty(t *testing.T) {
	profiles, err := LoadProfiles(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected empty profiles, got %v", profiles)
	}
}
