#!/usr/bin/env bash
# Miri proof run under strict provenance, Stacked and Tree Borrows.
#
# Runs the resolver, LPM view, validator and packet front suites, then every
# expected-UB test on its own, asserting Miri reports Undefined Behavior or,
# where the models differ, that the other model accepts the execution. The
# optional argument selects one model (stacked or tree); the default is
# both. MIRI_CARGO_FLAGS replaces the default --offline (CI passes --locked).
set -euo pipefail
cd "$(dirname "$0")/.."

toolchain=${MIRI_TOOLCHAIN:-nightly}
read -r -a cargo_flags <<<"${MIRI_CARGO_FLAGS:---offline}"
case ${1:-all} in
    stacked) models=("stacked:") ;;
    tree) models=("tree:-Zmiri-tree-borrows") ;;
    all) models=("stacked:" "tree:-Zmiri-tree-borrows") ;;
    *)
        echo "usage: $0 [stacked|tree|all]" >&2
        exit 2
        ;;
esac

run() {
    MIRIFLAGS="-Zmiri-strict-provenance $1" cargo "+$toolchain" miri test "${cargo_flags[@]}" "${@:2}"
}

# test name, then the expected outcome under stacked and tree borrows.
expectations=(
    "test_ub_block_resolver_freed_chunk ub ub"
    "test_ub_block_resolver_cross_block_overrun ub ub"
    "test_ub_whole_header_reference_across_c_write ub ub"
    "test_ub_stacked_slot_reference_provenance ub pass"
)

failures=0
for entry in "${models[@]}"; do
    name=${entry%%:*}
    flags=${entry#*:}
    echo "== $name borrows: positive suite"
    run "$flags" -p yanet-sdk --test lpm
    run "$flags" -p yanet-sys --test packet_front

    echo "== $name borrows: expected outcomes"
    for line in "${expectations[@]}"; do
        read -r test stacked tree <<<"$line"
        expected=$stacked
        [[ $name == tree ]] && expected=$tree
        if output=$(run "$flags" -p yanet-sdk --test lpm -- --ignored --exact "$test" 2>&1); then
            outcome=pass
        elif grep -q "Undefined Behavior" <<<"$output"; then
            outcome=ub
        else
            outcome=error
        fi
        verdict=ok
        [[ $outcome == "$expected" ]] || {
            verdict=MISMATCH
            failures=$((failures + 1))
        }
        printf '%-50s %-8s expected %-4s got %-5s %s\n' "$test" "$name" "$expected" "$outcome" "$verdict"
        if [[ $outcome == ub ]]; then
            grep -m1 "error: Undefined Behavior" <<<"$output" | sed 's/^/    /'
        elif [[ $outcome == error ]]; then
            tail -20 <<<"$output" | sed 's/^/    /'
        fi
    done
done
exit "$failures"
