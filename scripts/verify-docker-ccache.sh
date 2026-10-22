#!/usr/bin/env bash
# Two disposable containers must reuse one isolated host compiler cache.
#
# A cold compile must miss once and the repeat must hit once without another
# miss. A missing bind mount leaves the second container cold. Temporary state
# is removed on exit; the caller's shared cache is never read or cleared.
set -euo pipefail

justfile="${1:?usage: $0 <path to active justfile>}"

# Create the caller's cache root first: this probe runs before any container
# runner, so a fresh override root may not exist on the host yet.
cache_root="${DOCKER_CACHE_DIR:-/tmp}"
mkdir -p "${cache_root}"
probe_dir="$(mktemp -d "${cache_root}/ccache-probe.XXXXXX")"
trap 'rm -rf "$probe_dir"' EXIT

export DOCKER_CACHE_DIR="${probe_dir}"

# Fixed source path, flags, and output keep both compiles deterministic.
#
# Errexit prevents statistics from masking compilation failures. World-writable
# cache entries remain removable by the host user after the container exits.
compile_cmd='set -eu; umask 0000; mkdir -p /tmp/ccache-probe; printf "int ccache_probe(void) { return 42; }\n" > /tmp/ccache-probe/probe.c; ccache cc -c /tmp/ccache-probe/probe.c -o /tmp/ccache-probe/probe.o; ccache -s; ccache --print-stats'

cache_hits() {
    awk '$1 == "direct_cache_hit" { d = $2 + 0 } $1 == "preprocessed_cache_hit" { p = $2 + 0 } END { print d + p }'
}

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

fail() {
    printf 'ccache probe FAILED: %s\n' "$1" >&2
    printf '%s\n%s\n%s\n%s\n' \
        '--- cold container statistics ---' "${cold_out}" \
        '--- warm container statistics ---' "${warm_out}" >&2
    exit 1
}

cold_out="$(run_container)"
# The real runner must create every mount source as the invoking host user.
for cache in ccache gomodcache gocache; do
    [ -d "${probe_dir}/${cache}" ] && [ -O "${probe_dir}/${cache}" ] || {
        printf 'ccache probe FAILED: host does not own cache directory %s\n' "${cache}" >&2
        exit 1
    }
done
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
