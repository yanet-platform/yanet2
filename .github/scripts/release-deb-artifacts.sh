#!/bin/bash
set -euo pipefail

export LC_ALL=C

fail() {
    echo "release Debian artifact validation failed: $*" >&2
    exit 1
}

if [[ $# -lt 2 || $# -gt 3 ]]; then
    fail "usage: $0 <artifact-directory> <output-directory> [--include-2204]"
fi

artifact_dir=$1
output_dir=$2
include_2204=false
if [[ $# -eq 3 ]]; then
    [[ $3 == --include-2204 ]] || fail "unknown option: $3"
    include_2204=true
fi
[[ -d $artifact_dir ]] || fail "artifact directory does not exist: $artifact_dir"
artifact_dir=$(cd -- "$artifact_dir" && pwd -P)

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
verifier=${DEB_ARTIFACT_VERIFIER:-$script_dir/../../scripts/verify-deb-artifacts.sh}
[[ -x $verifier ]] || fail "verifier is not executable: $verifier"

expected_sets=(debs-24.04-amd64 debs-24.04-arm64)
if [[ $include_2204 == true ]]; then
    expected_sets+=(debs-22.04-amd64)
fi
mapfile -t expected_sets < <(printf '%s\n' "${expected_sets[@]}" | sort)
mapfile -t artifact_sets < <(
    find "$artifact_dir" -mindepth 1 -maxdepth 1 -type d -name 'debs-*' -printf '%f\n' | sort
)
if [[ "${artifact_sets[*]-}" != "${expected_sets[*]}" ]]; then
    fail "unexpected Debian artifact sets: ${artifact_sets[*]-}; expected: ${expected_sets[*]}"
fi

artifact_version=
for artifact_set in "${artifact_sets[@]}"; do
    case $artifact_set in
        debs-24.04-amd64|debs-22.04-amd64)
            expected_arch=amd64
            ;;
        debs-24.04-arm64)
            expected_arch=arm64
            ;;
        *)
            fail "unsupported Debian artifact set: $artifact_set"
            ;;
    esac

    mapfile -t changes_files < <(
        find "$artifact_dir/$artifact_set" -maxdepth 1 -type f -name '*.changes' -print | sort
    )
    (( ${#changes_files[@]} == 1 )) ||
        fail "expected exactly one .changes file in $artifact_set"
    changes_version=$(awk -F': ' '$1 == "Version" { print $2; exit }' "${changes_files[0]}")
    [[ -n $changes_version ]] || fail "missing Version in ${changes_files[0]}"
    if [[ -z $artifact_version ]]; then
        artifact_version=$changes_version
    elif [[ $changes_version != "$artifact_version" ]]; then
        fail "mixed release versions: $artifact_version and $changes_version"
    fi

    echo "Verifying $artifact_set"
    "$verifier" "$artifact_dir/$artifact_set" "$expected_arch"
done

release_sets=(debs-24.04-amd64 debs-24.04-arm64)
mkdir -p -- "$output_dir"
for release_set in "${release_sets[@]}"; do
    while IFS= read -r -d '' artifact; do
        destination=$output_dir/$(basename "$artifact")
        [[ ! -e $destination ]] || fail "artifact filename collision: $(basename "$artifact")"
        cp -- "$artifact" "$destination"
    done < <(
        find "$artifact_dir/$release_set" -type f \( -name '*.changes' -o -name '*.deb' -o -name '*.ddeb' \) -print0 |
            sort -z
    )
done

echo "Validated ${#artifact_sets[@]} Debian artifact sets at version $artifact_version"
echo "Flattened ${#release_sets[@]} release artifact sets into $output_dir"
