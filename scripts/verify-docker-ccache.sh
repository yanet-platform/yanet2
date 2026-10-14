#!/usr/bin/env bash
# Asserts that two disposable containers launched through the active justfile's
# shared Docker runner share one host compiler cache.
#
# A cold compile must register exactly one cache miss, and the identical repeat
# compile in a second container exactly one hit, direct or preprocessed, with
# no further miss. If the runner's cache bind mount is lost, the second
# container sees an empty container-local cache and the hit assertion fails
# before the suite starts. All state lives in one isolated directory that is
# removed on exit; the caller's shared cache is never read, cleared, or
# deleted.
set -euo pipefail

justfile="${1:?usage: $0 <path to active justfile>}"

# Create the caller's cache root first: this probe runs before any container
# runner, so a fresh override root may not exist on the host yet.
cache_root="${DOCKER_CACHE_DIR:-/tmp}"
mkdir -p "${cache_root}"
probe_dir="$(mktemp -d "${cache_root}/ccache-probe.XXXXXX")"
trap 'rm -rf "$probe_dir"' EXIT
# Pre-create the runner's mount sources so the Docker daemon never creates
# root-owned directories that the exit cleanup could not remove on Linux.
mkdir -p "${probe_dir}/ccache" "${probe_dir}/gomodcache" "${probe_dir}/gocache"

export DOCKER_CACHE_DIR="${probe_dir}"

# Fixed source path, flags, and output keep both compiles deterministic.
#
# The errexit prefix keeps a failed compile from being masked by the statistics
# commands, and the world-writable umask keeps cache entries written by the
# root container user removable for the host-side cleanup. Assertions read the
# machine counters; the human summary is printed only as evidence.
compile_cmd='set -eu; umask 0000; mkdir -p /tmp/ccache-probe; printf "int ccache_probe(void) { return 42; }\n" > /tmp/ccache-probe/probe.c; ccache cc -c /tmp/ccache-probe/probe.c -o /tmp/ccache-probe/probe.o; ccache -s; ccache --print-stats'

# Sums the direct and preprocessed hit counters from machine-readable cache
# statistics on stdin.
cache_hits() {
    awk '$1 == "direct_cache_hit" { d = $2 + 0 } $1 == "preprocessed_cache_hit" { p = $2 + 0 } END { print d + p }'
}

# Prints the miss counter from machine-readable cache statistics on stdin.
cache_misses() {
    awk '$1 == "cache_miss" { m = $2 + 0 } END { print m + 0 }'
}

# Runs one disposable compile container through the shared runner and prints
# its cache statistics; a failed run aborts the probe with the output kept.
run_container() {
    local out
    if ! out="$(just -f "${justfile}" _docker_run "" "${compile_cmd}")"; then
        printf 'ccache probe FAILED: compile container run failed\n%s\n' "${out}" >&2
        exit 1
    fi
    printf '%s\n' "${out}"
}

# Reports both containers' statistics and exits nonzero.
fail() {
    printf 'ccache probe FAILED: %s\n' "$1" >&2
    printf '%s\n%s\n%s\n%s\n' \
        '--- cold container statistics ---' "${cold_out}" \
        '--- warm container statistics ---' "${warm_out}" >&2
    exit 1
}

cold_out="$(run_container)"
warm_out="$(run_container)"
cold_hits="$(printf '%s\n' "${cold_out}" | cache_hits)"
cold_misses="$(printf '%s\n' "${cold_out}" | cache_misses)"
warm_hits="$(printf '%s\n' "${warm_out}" | cache_hits)"
warm_misses="$(printf '%s\n' "${warm_out}" | cache_misses)"

[ "${cold_hits}" = "0" ] && [ "${cold_misses}" = "1" ] \
    || fail "cold compile must miss exactly once, got hits=${cold_hits} misses=${cold_misses}"
[ "${warm_hits}" = "1" ] && [ "${warm_misses}" = "1" ] \
    || fail "warm compile must hit exactly once with no further miss, got hits=${warm_hits} misses=${warm_misses}: shared cache mount missing or ineffective"

printf 'ccache probe OK: cold hits=%s misses=%s; warm hits=%s (direct+preprocessed) misses=%s\n' \
    "${cold_hits}" "${cold_misses}" "${warm_hits}" "${warm_misses}"
