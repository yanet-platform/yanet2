#!/bin/bash
set -euo pipefail

export LC_ALL=C

repo_root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
helper=$repo_root/.github/scripts/release-deb-artifacts.sh
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

fail() {
    echo "release Debian artifact test failed: $*" >&2
    exit 1
}

fake_verifier=$test_root/fake-verifier
verification_log=$test_root/verifications
printf '%s\n' \
    '#!/bin/bash' \
    'set -euo pipefail' \
    '[[ $# -eq 2 ]] || exit 2' \
    'printf '\''%s %s\n'\'' "$(basename "$1")" "$2" >>"$VERIFY_LOG"' \
    >"$fake_verifier"
chmod +x "$fake_verifier"
export DEB_ARTIFACT_VERIFIER=$fake_verifier
export VERIFY_LOG=$verification_log

make_set() {
    local set_dir=$1
    local architecture=$2
    local version=$3
    local prefix=${4:-yanet}
    local changes_name=${5:-$prefix-$architecture.changes}
    mkdir -p "$set_dir"
    printf 'Version: %s\n' "$version" >"$set_dir/$changes_name"
    : >"$set_dir/${prefix}_${architecture}.deb"
    : >"$set_dir/${prefix}-dbgsym_${architecture}.ddeb"
}

expect_failure() {
    local expected=$1
    shift
    local log=$test_root/failure.log
    if "$@" >"$log" 2>&1; then
        fail "command unexpectedly succeeded: $*"
    fi
    grep -Fq -- "$expected" "$log" || {
        cat "$log" >&2
        fail "failure did not contain: $expected"
    }
}

valid=$test_root/valid
make_set "$valid/artifacts/debs-24.04-amd64" amd64 1.2.3 yanet
make_set "$valid/artifacts/debs-24.04-arm64" arm64 1.2.3 yanet
make_set "$valid/artifacts/debs-22.04-amd64" amd64 1.2.3 legacy legacy-22.04.changes
"$helper" "$valid/artifacts" "$valid/out" --include-2204 >/dev/null
[[ $(find "$valid/out" -type f | wc -l) -eq 6 ]] || fail 'unexpected release file count'
[[ ! -e "$valid/out/legacy_amd64.deb" ]] || fail '22.04 package was flattened'
[[ $(wc -l <"$verification_log") -eq 3 ]] || fail 'not all sets were verified'

unexpected=$test_root/unexpected
make_set "$unexpected/artifacts/debs-24.04-amd64" amd64 1.2.3
make_set "$unexpected/artifacts/debs-24.04-arm64" arm64 1.2.3
make_set "$unexpected/artifacts/debs-22.04-amd64" amd64 1.2.3
expect_failure 'unexpected Debian artifact sets' "$helper" "$unexpected/artifacts" "$unexpected/out"

mixed=$test_root/mixed
make_set "$mixed/artifacts/debs-24.04-amd64" amd64 1.2.3
make_set "$mixed/artifacts/debs-24.04-arm64" arm64 2.0.0
expect_failure 'mixed release versions' "$helper" "$mixed/artifacts" "$mixed/out"

collision=$test_root/collision
make_set "$collision/artifacts/debs-24.04-amd64" amd64 1.2.3 yanet shared.changes
make_set "$collision/artifacts/debs-24.04-arm64" arm64 1.2.3 yanet shared.changes
expect_failure 'artifact filename collision: shared.changes' "$helper" "$collision/artifacts" "$collision/out"

echo 'release Debian artifact helper tests passed'
