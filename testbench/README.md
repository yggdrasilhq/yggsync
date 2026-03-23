# yggsync Testbench

This bench exercises the failure modes that matter in the field:

- initial vault sync
- delete propagation
- explicit update after remote-only changes
- concurrent edits that trigger explicit worktree conflicts
- large media offload jobs that must not delete local files before the remote copy is confirmed

It uses mock "phone" and "laptop" directories on one machine, with plain local paths standing in for remote peers.

## Requirements

- `go`
## Run

```bash
./testbench/run.sh
./testbench/limitations.sh
./testbench/power_profile.sh
```

`run.sh` covers the supported baseline: initial commit, explicit remote update, delete propagation, and retained-copy safety.

`limitations.sh` is the sharper lab for conflict behavior under `worktree` semantics.

`power_profile.sh` is a synthetic schedule bench. It does not measure watts; it tells you how often fast and bulk jobs would fire under the current battery and unmetered-network gates. That gives you a concrete knob for reducing heat before you widen sync coverage.
