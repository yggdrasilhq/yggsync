package gate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

// Notify must never block the sync run: a Termux:API app that never answers
// its notification intent used to wedge whole runs (which then held the sync
// lock for days, so every later run bounced with "already running"). With a
// hanging termux-notification on PATH, Notify returns long before the fake
// command finishes.
func TestNotifyDoesNotBlockOnHungNotifier(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "termux-notification")
	script := "#!/bin/sh\nsleep 60\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	done := make(chan struct{})
	go func() {
		Notify("test title", "test body")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Notify blocked on a hung notification command")
	}
}

func TestNotifyNoopWithoutTermux(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir) // no termux-notification anywhere
	done := make(chan struct{})
	go func() {
		Notify("test title", "test body")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Notify should return immediately without termux-notification")
	}
}
