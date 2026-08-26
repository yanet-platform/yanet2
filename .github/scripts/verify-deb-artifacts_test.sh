#!/bin/bash
set -euo pipefail

export LC_ALL=C

repo_root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
verifier=$repo_root/scripts/verify-deb-artifacts.sh
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

fail() {
    echo "deb verifier test failed: $*" >&2
    exit 1
}

fake_bin=$test_root/bin
artifact_dir=$test_root/artifacts
mkdir -p "$fake_bin" "$artifact_dir"
printf '%s\n' '#!/bin/bash' 'exit 0' >"$fake_bin/dscverify"
printf '%s\n' \
    '#!/bin/bash' \
    'set -euo pipefail' \
    '[[ ${1:-} == --field ]] || exit 2' \
    'case "$(basename "$2"):$3" in' \
    '  yanet-test_1.2.3_amd64.deb:Package) printf "%s\\n" yanet-test ;;' \
    '  yanet-test_1.2.3_amd64.deb:Version) printf "%s\\n" 1.2.3 ;;' \
    '  yanet-test_1.2.3_amd64.deb:Architecture) printf "%s\\n" amd64 ;;' \
    '  yanet-test-dbgsym_1.2.3_amd64.ddeb:Package) printf "%s\\n" yanet-test-dbgsym ;;' \
    '  yanet-test-dbgsym_1.2.3_amd64.ddeb:Version) printf "%s\\n" 1.2.3 ;;' \
    '  yanet-test-dbgsym_1.2.3_amd64.ddeb:Architecture) printf "%s\\n" amd64 ;;' \
    '  yanet-test-dbgsym_1.2.3_amd64.ddeb:Depends) printf "%s\\n" "yanet-test (= 1.2.3)" ;;' \
    '  *) exit 1 ;;' \
    'esac' \
    >"$fake_bin/dpkg-deb"
chmod +x "$fake_bin/dscverify" "$fake_bin/dpkg-deb"
export PATH="$fake_bin:$PATH"

runtime=yanet-test_1.2.3_amd64.deb
dbgsym=yanet-test-dbgsym_1.2.3_amd64.ddeb
: >"$artifact_dir/$runtime"
: >"$artifact_dir/$dbgsym"

write_changes() {
    local mode=$1
    {
        printf 'Version: 1.2.3\n\nFiles:\n'
        printf ' 00000000000000000000000000000000 1 misc optional %s\n' "$runtime"
        if [[ $mode == files-duplicate ]]; then
            printf ' 00000000000000000000000000000000 1 misc optional %s\n' "$runtime"
        fi
        printf ' 00000000000000000000000000000000 1 misc optional %s\n\n' "$dbgsym"
        [[ $mode != missing-checksums ]] || return 0
        printf 'Checksums-Sha256:\n'
        [[ $mode != empty-checksums ]] || return 0
        printf ' 0000000000000000000000000000000000000000000000000000000000000000 1 %s\n' "$runtime"
        [[ $mode != mismatch ]] || return 0
        if [[ $mode == checksums-duplicate ]]; then
            printf ' 0000000000000000000000000000000000000000000000000000000000000000 1 %s\n' "$runtime"
        else
            printf ' 0000000000000000000000000000000000000000000000000000000000000000 1 %s\n' "$dbgsym"
        fi
    } >"$artifact_dir/test.changes"
}

expect_failure() {
    local mode=$1
    local expected=$2
    write_changes "$mode"
    if "$verifier" "$artifact_dir" amd64 >"$test_root/failure.log" 2>&1; then
        fail "$mode unexpectedly passed"
    fi
    grep -Fq -- "$expected" "$test_root/failure.log" || {
        cat "$test_root/failure.log" >&2
        fail "$mode did not report: $expected"
    }
}

write_changes valid
"$verifier" "$artifact_dir" amd64 >/dev/null
expect_failure missing-checksums 'missing Checksums-Sha256 package manifest'
expect_failure empty-checksums 'empty Checksums-Sha256 package manifest'
expect_failure files-duplicate 'duplicate package files in Files'
expect_failure checksums-duplicate 'duplicate package files in Checksums-Sha256'
expect_failure mismatch 'Files and Checksums-Sha256 package lists differ'

echo 'deb artifact verifier tests passed'
