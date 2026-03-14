#!/usr/bin/env bash
set -euo pipefail

FAST_PERIOD_MINUTES="${FAST_PERIOD_MINUTES:-180}"
BULK_PERIOD_MINUTES="${BULK_PERIOD_MINUTES:-720}"
FAST_MIN_BATTERY="${FAST_MIN_BATTERY:-50}"
BULK_MIN_BATTERY="${BULK_MIN_BATTERY:-65}"
HOURS="${HOURS:-24}"

fast_runs=0
bulk_runs=0
fast_skips=0
bulk_skips=0

battery_at_minute() {
  local minute="$1"
  local hour=$(( (minute / 60) % 24 ))
  case "$hour" in
    0|1|2|3|4|5) echo 82 ;;
    6|7|8) echo 58 ;;
    9|10|11|12|13|14|15|16) echo 41 ;;
    17|18|19) echo 63 ;;
    20|21|22|23) echo 74 ;;
  esac
}

charging_at_minute() {
  local minute="$1"
  local hour=$(( (minute / 60) % 24 ))
  case "$hour" in
    0|1|2|3|4|5|22|23) return 0 ;;
    *) return 1 ;;
  esac
}

unmetered_at_minute() {
  local minute="$1"
  local hour=$(( (minute / 60) % 24 ))
  case "$hour" in
    0|1|2|3|4|5|6|21|22|23) return 0 ;;
    *) return 1 ;;
  esac
}

should_run() {
  local min_battery="$1"
  local minute="$2"
  local battery
  battery="$(battery_at_minute "$minute")"
  if charging_at_minute "$minute"; then
    return 0
  fi
  if [[ "$battery" -ge "$min_battery" ]] && unmetered_at_minute "$minute"; then
    return 0
  fi
  return 1
}

printf 'Simulating %s hours with fast=%s min bulk=%s min fast-battery>=%s bulk-battery>=%s\n' \
  "$HOURS" "$FAST_PERIOD_MINUTES" "$BULK_PERIOD_MINUTES" "$FAST_MIN_BATTERY" "$BULK_MIN_BATTERY"

for ((minute=0; minute < HOURS * 60; minute++)); do
  if (( minute % FAST_PERIOD_MINUTES == 0 )); then
    if should_run "$FAST_MIN_BATTERY" "$minute"; then
      fast_runs=$((fast_runs + 1))
    else
      fast_skips=$((fast_skips + 1))
    fi
  fi

  if (( minute % BULK_PERIOD_MINUTES == 0 )); then
    if should_run "$BULK_MIN_BATTERY" "$minute"; then
      bulk_runs=$((bulk_runs + 1))
    else
      bulk_skips=$((bulk_skips + 1))
    fi
  fi
done

printf 'fast runs:  %s\n' "$fast_runs"
printf 'fast skips: %s\n' "$fast_skips"
printf 'bulk runs:  %s\n' "$bulk_runs"
printf 'bulk skips: %s\n' "$bulk_skips"
