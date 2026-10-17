#!/usr/bin/env bash
# Shows that ctest catches an added, a swapped and a retyped C field, both in
# a C library struct (systest, struct lpm) and in a module configuration
# (decap-systest, struct decap_module_config).
#
# Each variant compiles a suite against a scratch copy of the header with the
# drift injected (see the suites' build.rs); tracked headers are never
# modified. Every variant must fail, and the final runs against the real
# headers must pass.
set -uo pipefail

poc="$(cd "$(dirname "$0")/.." && pwd)"
status=0
for suite in systest:common/lpm.h decap-systest:modules/decap/dataplane/config.h; do
    package="${suite%%:*}"
    header="${suite#*:}"
    for kind in added swapped retyped; do
        echo "=== $package: $kind field in $header"
        if output="$(YANET_CTEST_DRIFT="$kind" cargo run --quiet --manifest-path "$poc/Cargo.toml" -p "$package" 2>&1)"; then
            echo "NOT CAUGHT"
            status=1
        else
            grep -E '^bad |error: .*(incompatible|differ in signedness)' <<<"$output" \
                | sed -E 's/^.*(error: )/\1/; s/ \[-Werror[^]]*\]//' | head -6
            echo "caught"
        fi
    done
    echo "=== $package: real headers"
    cargo run --quiet --manifest-path "$poc/Cargo.toml" -p "$package" || status=1
done
exit "$status"
