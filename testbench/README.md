# yggsync Testbench

This bench exercises the failure modes that matter in the field:

- initial vault sync
- delete propagation
- rename propagation
- concurrent edits that trigger bisync conflicts
- large media offload jobs that must not delete local files before the remote copy is confirmed

It uses mock "phone" and "laptop" directories on one machine, with `rclone` local paths standing in for remote peers.

## Requirements

- `go`
- `rclone`

## Run

```bash
./testbench/run.sh
./testbench/limitations.sh
./testbench/power_profile.sh
```

`run.sh` covers the supported baseline: initial sync, delete propagation, and retained-copy safety.

`limitations.sh` is the sharper lab for rename/conflict behavior. It currently exists to reproduce the tricky bisync cases that still need better design or different product guidance.

`power_profile.sh` is a synthetic schedule bench. It does not measure watts; it tells you how often fast and bulk jobs would fire under the current battery and unmetered-network gates. That gives you a concrete knob for reducing heat before you widen sync coverage.
