## Potential resilience tweaks (not enabled by default)

- Add per-job rclone retry/backoff flags (e.g., `--retries`, `--low-level-retries`, `--retries-sleep`) for weak links (tailscale/hotspot).
- Add optional lock cleanup + one retry when transient SMB errors occur (connection reset/timeout).
- Add a “network profile” toggle in config (aggressive vs. gentle retry sets) to avoid per-job flag churn.
- Keep the current behavior as default (lean, relies on rclone’s built-ins); enable tweaks only if field failures appear.
