#!/usr/bin/env bash
set -euo pipefail

OUTDIR="${1:-}"
LABEL="${2:-run}"
INTERVAL="${3:-10}"

if [[ -z "$OUTDIR" ]]; then
  echo "Usage: $0 <outdir> [label] [interval_seconds]" >&2
  exit 2
fi

mkdir -p "$OUTDIR"
SAMPLES_FILE="$OUTDIR/samples.tsv"

if [[ ! -f "$SAMPLES_FILE" ]]; then
  printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n" \
    "timestamp" \
    "label" \
    "load1" \
    "load5" \
    "load15" \
    "mem_available_kb" \
    "swap_free_kb" \
    "cgroup_mem_current" \
    "cgroup_mem_max" \
    "cgroup_cpu_max" \
    "qemu_count" \
    "qemu_rss_kb" \
    "qemu_cpu_pct" \
    "go_test_count" \
    "root_fs_free_kb" \
    "work_fs_free_kb" \
    > "$SAMPLES_FILE"
fi

read_cgroup_value() {
  local path="$1"
  if [[ -r "$path" ]]; then
    cat "$path"
  else
    echo "n/a"
  fi
}

collect_sample() {
  local timestamp load1 load5 load15 mem_avail swap_free mem_current mem_max cpu_max
  local qemu_count qemu_rss qemu_cpu go_count root_free work_free

  timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  read -r load1 load5 load15 _ < /proc/loadavg
  mem_avail="$(awk '/MemAvailable:/ {print $2}' /proc/meminfo)"
  swap_free="$(awk '/SwapFree:/ {print $2}' /proc/meminfo)"
  mem_current="$(read_cgroup_value /sys/fs/cgroup/memory.current)"
  mem_max="$(read_cgroup_value /sys/fs/cgroup/memory.max)"
  cpu_max="$(read_cgroup_value /sys/fs/cgroup/cpu.max)"

  qemu_count="$(pgrep -fc 'qemu-system-x86_64' || true)"
  if [[ "$qemu_count" -gt 0 ]]; then
    qemu_rss="$(ps -o rss= -C qemu-system-x86_64 | awk '{sum+=$1} END {print sum+0}')"
    qemu_cpu="$(ps -o pcpu= -C qemu-system-x86_64 | awk '{sum+=$1} END {print sum+0}')"
  else
    qemu_rss=0
    qemu_cpu=0
  fi

  go_count="$(pgrep -fc 'go test' || true)"
  root_free="$(df -k / | awk 'NR==2 {print $4}')"
  work_free="$(df -k "$PWD" | awk 'NR==2 {print $4}')"

  printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n" \
    "$timestamp" \
    "$LABEL" \
    "$load1" \
    "$load5" \
    "$load15" \
    "${mem_avail:-0}" \
    "${swap_free:-0}" \
    "$mem_current" \
    "$mem_max" \
    "$cpu_max" \
    "$qemu_count" \
    "$qemu_rss" \
    "$qemu_cpu" \
    "$go_count" \
    "${root_free:-0}" \
    "${work_free:-0}" \
    >> "$SAMPLES_FILE"
}

write_snapshot() {
  local timestamp filename_timestamp snapshot_file
  timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  filename_timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
  snapshot_file="$OUTDIR/snapshot-${LABEL}-${filename_timestamp}.log"

  {
    echo "=== TIMESTAMP ==="
    echo "$timestamp"
    echo
    echo "=== UPTIME ==="
    uptime || true
    echo
    echo "=== FREE ==="
    free -h || true
    echo
    echo "=== MEMINFO ==="
    awk '/MemAvailable:|SwapFree:|HugePages_/ {print}' /proc/meminfo || true
    echo
    echo "=== DF ==="
    df -h || true
    echo
    echo "=== CGROUP LIMITS ==="
    echo "memory.current=$(read_cgroup_value /sys/fs/cgroup/memory.current)"
    echo "memory.max=$(read_cgroup_value /sys/fs/cgroup/memory.max)"
    echo "cpu.max=$(read_cgroup_value /sys/fs/cgroup/cpu.max)"
    echo
    echo "=== QEMU PROCESSES ==="
    pgrep -af qemu-system-x86_64 || true
    echo
    echo "=== GO TEST PROCESSES ==="
    pgrep -af 'go test' || true
    echo
    echo "=== TOP RSS ==="
    ps -eo pid,ppid,pcpu,pmem,rss,command --sort=-rss | head -n 20 || true
    echo
    echo "=== TOP CPU ==="
    ps -eo pid,ppid,pcpu,pmem,rss,command --sort=-pcpu | head -n 20 || true
  } > "$snapshot_file"
}

write_kernel_logs() {
  {
    echo "=== DMESG TAIL ==="
    dmesg -T | tail -n 200 || true
    echo
    echo "=== JOURNALCTL KERNEL TAIL ==="
    if command -v journalctl >/dev/null 2>&1; then
      journalctl -k -n 200 || true
    else
      echo "journalctl not available"
    fi
  } > "$OUTDIR/kernel-${LABEL}.log"
}

handle_exit() {
  write_snapshot
  write_kernel_logs
}

handle_signal() {
  trap - EXIT INT TERM
  handle_exit
  exit 0
}

trap handle_exit EXIT
trap handle_signal INT TERM

write_snapshot
while true; do
  collect_sample
  sleep "$INTERVAL"
done
