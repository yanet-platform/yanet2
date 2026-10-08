#!/usr/bin/env bash
# Shows that ctest catches an added, a swapped and a retyped C field.
#
# Each variant compiles the ctest suite against a scratch copy of
# common/lpm.h with the drift injected (see systest/build.rs); the tracked
# header is never modified. Every variant must fail, and the final run
# against the real headers must pass.
set -uo pipefail

poc="$(cd "$(dirname "$0")/.." && pwd)"
status=0
for kind in added swapped retyped; do
    echo "=== drift: $kind field in struct lpm"
    if output="$(YANET_CTEST_DRIFT="$kind" cargo run --quiet --manifest-path "$poc/Cargo.toml" -p systest 2>&1)"; then
        echo "NOT CAUGHT"
        status=1
    else
        grep -E '^bad |error: .*(incompatible|differ in signedness)' <<<"$output" \
            | sed -E 's/^.*(error: )/\1/; s/ \[-Werror[^]]*\]//' | head -6
        echo "caught"
    fi
done
echo "=== real headers"
cargo run --quiet --manifest-path "$poc/Cargo.toml" -p systest || status=1
exit "$status"
