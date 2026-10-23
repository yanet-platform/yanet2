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
# Errexit prevents statistics from masking compilation failures. The compile
# exercises the runner's normal permissions: the object mode and the cache
# directory modes are asserted for both runs, guarding against a umask
# regression of either direction.
compile_cmd='set -eu; mkdir -p /tmp/ccache-probe; printf "int ccache_probe(void) { return 42; }\n" > /tmp/ccache-probe/probe.c; ccache cc -c /tmp/ccache-probe/probe.c -o /tmp/ccache-probe/probe.o; ccache -s; ccache --print-stats; printf "probe.o mode: "; stat -c "%a" /tmp/ccache-probe/probe.o; find /ccache -mindepth 1 -type d -printf "ccache dir mode: %m %p\n" | sort'

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

# Cold and cached compiler output must keep ordinary permissions; every
# cache directory must allow the host user to traverse and remove entries.
for run in cold warm; do
    if [ "${run}" = cold ]; then out="${cold_out}"; else out="${warm_out}"; fi
    object_mode="$(printf '%s\n' "${out}" | sed -n 's/^probe\.o mode: //p')"
    [ -n "${object_mode}" ] \
        || fail "${run} container must report the compiled object mode"
    [ "$(( 8#${object_mode} & 002 ))" -eq 0 ] \
        || fail "${run} compiled object must not be world-writable, mode ${object_mode}"
    shard_modes="$(printf '%s\n' "${out}" | sed -n 's/^ccache dir mode: \([0-7]*\) .*$/\1/p')"
    [ -n "${shard_modes}" ] \
        || fail "${run} container must report ccache cache directory modes"
    for mode in ${shard_modes}; do
        [ "$(( 8#${mode} & 003 ))" -eq 3 ] \
            || fail "${run} cache directory must be other-writable and searchable, mode ${mode}"
    done
done
cache_tree="${probe_dir}/ccache"
[ -d "${cache_tree}" ] || fail "warm container must populate the isolated ccache cache tree"
[ -n "$(ls -A "${cache_tree}")" ] \
    || fail "container-written ccache cache tree must not be empty"
find "${cache_tree:?}" -mindepth 1 -delete \
    || fail "host user must be able to remove container-written ccache cache entries"

printf 'ccache probe OK: cold hits=%s misses=%s; warm hits=%s (direct+preprocessed) misses=%s\n' \
    "${cold_hits}" "${cold_misses}" "${warm_hits}" "${warm_misses}"
