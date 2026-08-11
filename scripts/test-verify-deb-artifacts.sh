#!/bin/bash
set -euo pipefail

if ! script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd); then
    echo "could not resolve the test script directory" >&2
    exit 1
fi
verifier=$script_dir/verify-deb-artifacts.sh
if ! tmpdir=$(mktemp -d); then
    echo "could not create temporary directory" >&2
    exit 1
fi
trap 'rm -rf "$tmpdir"' EXIT

make_package() {
    set -e

    local case_dir=$1
    local package_name=$2
    local version=$3
    local depends=$4
    local extension=$5
    local root="$case_dir/${package_name}-${extension#\.}"
    local output="$case_dir/${package_name}_${version}_all$extension"

    mkdir -p "$root/DEBIAN"
    {
        printf 'Package: %s\n' "$package_name"
        printf 'Version: %s\n' "$version"
        printf 'Architecture: all\n'
        printf 'Maintainer: YANET CI <ci@yanet-platform.dev>\n'
        if [[ -n $depends ]]; then
            printf 'Depends: %s\n' "$depends"
        fi
        printf 'Description: package verifier fixture\n fixture\n'
    } >"$root/DEBIAN/control"
    dpkg-deb --build --root-owner-group "$root" "$output" >/dev/null
}

make_changes() {
    set -e

    local case_name=$1
    local case_dir=$tmpdir/$case_name
    local version=$2
    shift 2
    local output="$case_dir/yanet2_${version}_source.changes"

    {
        printf 'Format: 1.8\n'
        printf 'Version: %s\n' "$version"
        printf 'Files:\n'
        for package_file_name in "$@"; do
            printf ' 00000000000000000000000000000000 0 %s\n' "$package_file_name"
        done
        printf 'Checksums-Sha256:\n'
        for package_file_name in "$@"; do
            printf ' %064d 0 %s\n' 0 "$package_file_name"
        done
    } >"$output"
}

expect_pass() {
    set -e

    local case_name=$1
    local case_dir=$tmpdir/$case_name
    "$verifier" "$case_dir"
}

expect_fail() {
    set -e

    local case_name=$1
    local case_dir=$tmpdir/$case_name
    if "$verifier" "$case_dir"; then
        echo "expected verifier failure for $case_name" >&2
        exit 1
    fi
}

mkdir -p "$tmpdir/valid"
make_package "$tmpdir/valid" yanet2 1.0.0 '' .deb
make_package "$tmpdir/valid" yanet2-dbgsym 1.0.0 'yanet2 (= 1.0.0), libc6' .ddeb
make_package "$tmpdir/valid" yanet2-cli 1.0.0 '' .deb
make_package "$tmpdir/valid" yanet2-cli-dbgsym 1.0.0 'yanet2-cli (= 1.0.0)' .ddeb
make_package "$tmpdir/valid" yanet2-data 1.0.0 '' .deb
make_changes valid 1.0.0 \
    yanet2_1.0.0_all.deb \
    yanet2-dbgsym_1.0.0_all.ddeb \
    yanet2-cli_1.0.0_all.deb \
    yanet2-cli-dbgsym_1.0.0_all.ddeb \
    yanet2-data_1.0.0_all.deb
expect_pass valid

mkdir -p "$tmpdir/missing-manifest"
make_package "$tmpdir/missing-manifest" yanet2 1.0.0 '' .deb
make_package "$tmpdir/missing-manifest" yanet2-dbgsym 1.0.0 'yanet2 (= 1.0.0)' .ddeb
expect_fail missing-manifest

mkdir -p "$tmpdir/duplicate-manifest"
make_package "$tmpdir/duplicate-manifest" yanet2 1.0.0 '' .deb
make_package "$tmpdir/duplicate-manifest" yanet2-dbgsym 1.0.0 'yanet2 (= 1.0.0)' .ddeb
make_changes duplicate-manifest 1.0.0 \
    yanet2_1.0.0_all.deb \
    yanet2-dbgsym_1.0.0_all.ddeb
cp "$tmpdir/duplicate-manifest/yanet2_1.0.0_source.changes" \
    "$tmpdir/duplicate-manifest/second.changes"
expect_fail duplicate-manifest

mkdir -p "$tmpdir/unmanifested-package"
make_package "$tmpdir/unmanifested-package" yanet2 1.0.0 '' .deb
make_package "$tmpdir/unmanifested-package" yanet2-dbgsym 1.0.0 'yanet2 (= 1.0.0)' .ddeb
make_package "$tmpdir/unmanifested-package" yanet2-extra 1.0.0 '' .deb
make_changes unmanifested-package 1.0.0 \
    yanet2_1.0.0_all.deb \
    yanet2-dbgsym_1.0.0_all.ddeb
expect_fail unmanifested-package

mkdir -p "$tmpdir/missing-dbgsym"
make_package "$tmpdir/missing-dbgsym" yanet2 1.0.0 '' .deb
make_changes missing-dbgsym 1.0.0 yanet2_1.0.0_all.deb
expect_fail missing-dbgsym

mkdir -p "$tmpdir/omitted-one-dbgsym"
make_package "$tmpdir/omitted-one-dbgsym" yanet2 1.0.0 '' .deb
make_package "$tmpdir/omitted-one-dbgsym" yanet2-dbgsym 1.0.0 'yanet2 (= 1.0.0)' .ddeb
make_package "$tmpdir/omitted-one-dbgsym" yanet2-cli 1.0.0 '' .deb
make_package "$tmpdir/omitted-one-dbgsym" yanet2-cli-dbgsym 1.0.0 'yanet2-cli (= 1.0.0)' .ddeb
rm "$tmpdir/omitted-one-dbgsym/yanet2-cli-dbgsym_1.0.0_all.ddeb"
make_changes omitted-one-dbgsym 1.0.0 \
    yanet2_1.0.0_all.deb \
    yanet2-dbgsym_1.0.0_all.ddeb \
    yanet2-cli_1.0.0_all.deb \
    yanet2-cli-dbgsym_1.0.0_all.ddeb
expect_fail omitted-one-dbgsym

mkdir -p "$tmpdir/mismatched-version"
make_package "$tmpdir/mismatched-version" yanet2 1.0.0 '' .deb
make_package "$tmpdir/mismatched-version" yanet2-dbgsym 2.0.0 'yanet2 (= 2.0.0)' .ddeb
make_changes mismatched-version 1.0.0 \
    yanet2_1.0.0_all.deb \
    yanet2-dbgsym_2.0.0_all.ddeb
expect_fail mismatched-version

mkdir -p "$tmpdir/duplicate-package"
make_package "$tmpdir/duplicate-package" yanet2 1.0.0 '' .deb
cp "$tmpdir/duplicate-package/yanet2_1.0.0_all.deb" "$tmpdir/duplicate-package/renamed.deb"
make_package "$tmpdir/duplicate-package" yanet2-dbgsym 1.0.0 'yanet2 (= 1.0.0)' .ddeb
make_changes duplicate-package 1.0.0 \
    yanet2_1.0.0_all.deb \
    renamed.deb \
    yanet2-dbgsym_1.0.0_all.ddeb
expect_fail duplicate-package

mkdir -p "$tmpdir/missing-runtime"
make_package "$tmpdir/missing-runtime" yanet2-cli 1.0.0 '' .deb
make_package "$tmpdir/missing-runtime" yanet2-dbgsym 1.0.0 'yanet2 (= 1.0.0)' .ddeb
make_changes missing-runtime 1.0.0 \
    yanet2-cli_1.0.0_all.deb \
    yanet2-dbgsym_1.0.0_all.ddeb
expect_fail missing-runtime

mkdir -p "$tmpdir/wrong-dependency"
make_package "$tmpdir/wrong-dependency" yanet2 1.0.0 '' .deb
make_package "$tmpdir/wrong-dependency" yanet2-dbgsym 1.0.0 'yanet2 (= 1.0.1)' .ddeb
make_changes wrong-dependency 1.0.0 \
    yanet2_1.0.0_all.deb \
    yanet2-dbgsym_1.0.0_all.ddeb
expect_fail wrong-dependency

mkdir -p "$tmpdir/non-exact-dependency"
make_package "$tmpdir/non-exact-dependency" yanet2 1.0.0 '' .deb
make_package "$tmpdir/non-exact-dependency" yanet2-dbgsym 1.0.0 'yanet2 (>= 1.0.0)' .ddeb
make_changes non-exact-dependency 1.0.0 \
    yanet2_1.0.0_all.deb \
    yanet2-dbgsym_1.0.0_all.ddeb
expect_fail non-exact-dependency

echo "Debian artifact verifier regression tests passed"
