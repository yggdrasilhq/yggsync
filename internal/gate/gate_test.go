package gate

import "testing"

func TestDecideDisabled(t *testing.T) {
	if skip, _ := Decide(Policy{Enabled: false}, Status{Percentage: 1, TempC: 99}); skip {
		t.Fatal("disabled policy must never skip")
	}
}

func TestDecideBatteryLow(t *testing.T) {
	p := Policy{Enabled: true, BatteryMinPercent: 30}
	if skip, why := Decide(p, Status{Percentage: 20, Charging: false}); !skip || why == "" {
		t.Fatalf("expected low-battery skip, got skip=%v why=%q", skip, why)
	}
	// Charging overrides the low-battery skip.
	if skip, _ := Decide(p, Status{Percentage: 20, Charging: true}); skip {
		t.Fatal("charging should not skip on low battery")
	}
	// Above threshold does not skip.
	if skip, _ := Decide(p, Status{Percentage: 40, Charging: false}); skip {
		t.Fatal("above threshold should not skip")
	}
}

func TestDecideHot(t *testing.T) {
	p := Policy{Enabled: true, MaxBatteryTempC: 45}
	if skip, why := Decide(p, Status{Percentage: 90, TempC: 46, Charging: true}); !skip || why == "" {
		t.Fatalf("expected hot skip even while charging, got skip=%v why=%q", skip, why)
	}
	if skip, _ := Decide(p, Status{Percentage: 90, TempC: 40}); skip {
		t.Fatal("cool device should not skip")
	}
}

func TestDecideRequireCharging(t *testing.T) {
	p := Policy{Enabled: true, RequireCharging: true, BatteryMinPercent: 0}
	if skip, _ := Decide(p, Status{Percentage: 99, Charging: false}); !skip {
		t.Fatal("require_charging should skip when unplugged")
	}
	if skip, _ := Decide(p, Status{Percentage: 99, Charging: true}); skip {
		t.Fatal("require_charging should allow when charging")
	}
}

func TestDefaultsApplied(t *testing.T) {
	// Enabled with all-zero thresholds should still gate using defaults.
	p := Policy{Enabled: true}
	if skip, _ := Decide(p, Status{Percentage: 5, Charging: false}); !skip {
		t.Fatal("default battery threshold should skip at 5%")
	}
	if skip, _ := Decide(p, Status{TempC: 50, Percentage: 90, Charging: true}); !skip {
		t.Fatal("default temp threshold should skip at 50C")
	}
}
