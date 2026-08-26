#!/bin/bash
set -euo pipefail

export LC_ALL=C

fail() {
    echo "deb artifact verification failed: $*" >&2
    exit 1
}

if [[ $# -lt 1 || $# -gt 2 ]]; then
    fail "usage: $0 <artifact-directory> [expected-architecture]"
fi

artifact_dir=$1
expected_arch=${2:-}
[[ -d $artifact_dir ]] || fail "artifact directory does not exist: $artifact_dir"
if [[ -n $expected_arch && ! $expected_arch =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
    fail "invalid expected architecture: $expected_arch"
fi

command -v dscverify >/dev/null 2>&1 || fail "dscverify is required"
command -v dpkg-deb >/dev/null 2>&1 || fail "dpkg-deb is required"

mapfile -d '' changes_files < <(find "$artifact_dir" -type f -name '*.changes' -print0)
(( ${#changes_files[@]} == 1 )) || fail "expected exactly one .changes file"
changes_file=${changes_files[0]}
changes_dir=${changes_file%/*}
changes_name=${changes_file##*/}

if ! (cd "$changes_dir" && dscverify --nosigcheck "$changes_name"); then
    fail "dscverify rejected $changes_file"
fi

if ! changes_version=$(awk -F': ' '$1 == "Version" { print $2; exit }' "$changes_file"); then
    fail "could not read Version from $changes_file"
fi
[[ -n $changes_version ]] || fail "empty Version in $changes_file"

extract_manifest_packages() {
    local section=$1
    awk -v section="$section" '
        $0 == section ":" { in_section = 1; found_section = 1; next }
        in_section && /^[^[:space:]][^:]*:/ { in_section = 0 }
        in_section && /^[[:space:]]+/ && $NF ~ /\.(deb|ddeb)$/ {
            package = $NF
            sub(/^.*\//, "", package)
            print package
        }
        END {
            exit found_section ? 0 : 1
        }
    ' "$changes_file"
}

if ! files_manifest=$(extract_manifest_packages Files); then
    fail "missing Files package manifest in $changes_file"
fi
if ! checksums_manifest=$(extract_manifest_packages Checksums-Sha256); then
    fail "missing Checksums-Sha256 package manifest in $changes_file"
fi
mapfile -t files_packages < <(printf '%s\n' "$files_manifest" | awk 'NF' | sort)
mapfile -t checksums_packages < <(printf '%s\n' "$checksums_manifest" | awk 'NF' | sort)
(( ${#files_packages[@]} > 0 )) || fail "empty Files package manifest in $changes_file"
(( ${#checksums_packages[@]} > 0 )) || fail "empty Checksums-Sha256 package manifest in $changes_file"
mapfile -t duplicate_files < <(printf '%s\n' "${files_packages[@]}" | uniq -d)
mapfile -t duplicate_checksums < <(printf '%s\n' "${checksums_packages[@]}" | uniq -d)
(( ${#duplicate_files[@]} == 0 )) ||
    fail "duplicate package files in Files of $changes_file: ${duplicate_files[*]}"
(( ${#duplicate_checksums[@]} == 0 )) ||
    fail "duplicate package files in Checksums-Sha256 of $changes_file: ${duplicate_checksums[*]}"
[[ "${files_packages[*]}" == "${checksums_packages[*]}" ]] ||
    fail "Files and Checksums-Sha256 package lists differ in $changes_file"

declared_packages=("${files_packages[@]}")

declare -A declared_package
for package_file_name in "${declared_packages[@]}"; do
    declared_package[$package_file_name]=1
done

mapfile -d '' package_files < <(
    find "$artifact_dir" -type f \( -name '*.deb' -o -name '*.ddeb' \) -print0 | sort -z
)
(( ${#package_files[@]} > 0 )) || fail "no .deb or .ddeb files found"

declare -A actual_package
for package_file_path in "${package_files[@]}"; do
    package_file_name=${package_file_path##*/}
    [[ ${declared_package[$package_file_name]+present} == present ]] ||
        fail "unmanifested package file: $package_file_name"
    [[ ${actual_package[$package_file_name]+present} != present ]] ||
        fail "duplicate package file: $package_file_name"
    actual_package[$package_file_name]=$package_file_path
done

for package_file_name in "${declared_packages[@]}"; do
    [[ ${actual_package[$package_file_name]+present} == present ]] ||
        fail "missing package file declared by $changes_file: $package_file_name"
done

read_field() {
    local field=$1
    local package_file_path=$2
    dpkg-deb --field "$package_file_path" "$field"
}

declare -A package_file
declare -A package_version
runtime_count=0
dbgsym_count=0
dbgsym_ddeb_count=0
artifact_version=

for package_file_name in "${declared_packages[@]}"; do
    package_file_path=${actual_package[$package_file_name]}
    if ! package_name=$(read_field Package "$package_file_path"); then
        fail "could not read Package from $package_file_path"
    fi
    if ! version=$(read_field Version "$package_file_path"); then
        fail "could not read Version from $package_file_path"
    fi
    if ! package_arch=$(read_field Architecture "$package_file_path"); then
        fail "could not read Architecture from $package_file_path"
    fi
    [[ -n $package_name ]] || fail "empty Package in $package_file_path"
    [[ -n $version ]] || fail "empty Version in $package_file_path"
    if [[ -n $expected_arch && $package_arch != "$expected_arch" ]]; then
        fail "unexpected architecture for $(basename "$package_file_path"): $package_arch (expected $expected_arch)"
    fi

    [[ ${package_file[$package_name]+present} != present ]] ||
        fail "duplicate package name: $package_name"
    package_file[$package_name]=$package_file_path
    package_version[$package_name]=$version

    if [[ -z $artifact_version ]]; then
        artifact_version=$version
    elif [[ $version != "$artifact_version" ]]; then
        fail "mixed package versions: $artifact_version and $version"
    fi

    if [[ $package_name == *-dbgsym ]]; then
        dbgsym_count=$((dbgsym_count + 1))
        if [[ $package_file_path == *.ddeb ]]; then
            dbgsym_ddeb_count=$((dbgsym_ddeb_count + 1))
        fi
    else
        runtime_count=$((runtime_count + 1))
    fi
done

[[ $artifact_version == "$changes_version" ]] ||
    fail "package version $artifact_version does not match $changes_file version $changes_version"
(( runtime_count > 0 )) || fail "no ordinary packages found"
(( dbgsym_count > 0 )) || fail "no dbgsym packages found"
(( dbgsym_ddeb_count > 0 )) || fail "no dbgsym .ddeb files found"

mapfile -t package_names < <(printf '%s\n' "${!package_file[@]}" | sort)
for package_name in "${package_names[@]}"; do
    [[ $package_name == *-dbgsym ]] || continue

    runtime_name=${package_name%-dbgsym}
    [[ ${package_file[$runtime_name]+present} == present ]] ||
        fail "missing runtime package $runtime_name for $package_name"

    if ! depends=$(read_field Depends "${package_file[$package_name]}"); then
        fail "could not read Depends from ${package_file[$package_name]}"
    fi
    expected_dependency="$runtime_name (= ${package_version[$package_name]})"
    if ! printf '%s\n' "$depends" | awk -F, -v expected="$expected_dependency" '
        {
            for (i = 1; i <= NF; i++) {
                dependency = $i
                gsub(/^[[:space:]]+|[[:space:]]+$/, "", dependency)
                if (dependency == expected) {
                    found = 1
                }
            }
        }
        END {
            exit found ? 0 : 1
        }
    '; then
        fail "$package_name must depend on $expected_dependency"
    fi
done

echo "Verified $((runtime_count + dbgsym_count)) Debian packages at version $artifact_version"
