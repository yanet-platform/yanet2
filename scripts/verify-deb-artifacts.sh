#!/bin/bash
set -euo pipefail

fail() {
    echo "deb artifact verification failed: $*" >&2
    exit 1
}

if [[ $# -ne 1 ]]; then
    fail "usage: $0 <artifact-directory>"
fi

artifact_dir=$1
[[ -d $artifact_dir ]] || fail "artifact directory does not exist: $artifact_dir"

if ! tmpdir=$(mktemp -d); then
    fail "could not create temporary directory"
fi
trap 'rm -rf "$tmpdir"' EXIT

if ! find "$artifact_dir" -type f -name '*.changes' -print0 >"$tmpdir/changes-files"; then
    fail "could not enumerate .changes files"
fi
mapfile -d '' changes_files <"$tmpdir/changes-files"
(( ${#changes_files[@]} == 1 )) || fail "expected exactly one .changes file"
changes_file=${changes_files[0]}

read_changes_field() {
    set -e

    local field=$1
    local file=$2
    awk -v field="$field" '
        index($0, field ":") == 1 {
            sub(/^[^:]+:[[:space:]]*/, "")
            print
            exit
        }
    ' "$file"
}

extract_changes_packages() {
    set -e

    local section=$1
    local file=$2
    awk -v section="$section" '
        $0 == section ":" {
            in_section = 1
            found_section = 1
            next
        }
        in_section && /^[^[:space:]][^:]*:/ {
            in_section = 0
        }
        in_section && /^[[:space:]]+/ && $NF ~ /\.(deb|ddeb)$/ {
            print $NF
        }
        END {
            if (!found_section) {
                exit 1
            }
        }
    ' "$file"
}

if ! changes_version=$(read_changes_field Version "$changes_file"); then
    fail "could not read Version from $changes_file"
fi
[[ -n $changes_version ]] || fail "empty Version in $changes_file"

if ! files_packages=$(extract_changes_packages Files "$changes_file"); then
    fail "could not read package files from Files in $changes_file"
fi
if ! checksums_packages=$(extract_changes_packages Checksums-Sha256 "$changes_file"); then
    fail "could not read package files from Checksums-Sha256 in $changes_file"
fi

validate_manifest_packages() {
    set -e

    local section=$1
    local section_name=$2
    if ! printf '%s\n' "$section" | awk '
        BEGIN {
            valid = 1
        }
        NF {
            if ($0 !~ /^[^[:space:]]+\.(deb|ddeb)$/ || ++seen[$0] > 1) {
                valid = 0
                next
            }
            count++
        }
        END {
            exit valid == 0 || count == 0 ? 1 : 0
        }
    '; then
        fail "invalid or duplicate package files in $section_name of $changes_file"
    fi
}

validate_manifest_packages "$files_packages" Files
validate_manifest_packages "$checksums_packages" Checksums-Sha256

printf '%s\n' "$files_packages" | sort >"$tmpdir/files-packages"
printf '%s\n' "$checksums_packages" | sort >"$tmpdir/checksums-packages"
if ! cmp -s "$tmpdir/files-packages" "$tmpdir/checksums-packages"; then
    fail "Files and Checksums-Sha256 package lists differ in $changes_file"
fi

mapfile -t declared_packages <"$tmpdir/files-packages"
declare -A declared_package
for package_file_name in "${declared_packages[@]}"; do
    declared_package[$package_file_name]=1
done

if ! find "$artifact_dir" -type f \( -name '*.deb' -o -name '*.ddeb' \) -print0 >"$tmpdir/package-files"; then
    fail "could not enumerate package files"
fi
mapfile -d '' package_files <"$tmpdir/package-files"
(( ${#package_files[@]} > 0 )) || fail "no .deb or .ddeb files found"

declare -A actual_package
for package_file_path in "${package_files[@]}"; do
    package_file_name=${package_file_path##*/}
    [[ ${declared_package[$package_file_name]+present} == present ]] ||
        fail "unmanifested package file: $package_file_name"
    if [[ ${actual_package[$package_file_name]+present} ]]; then
        fail "duplicate package file: $package_file_name"
    fi
    actual_package[$package_file_name]=$package_file_path
done

for package_file_name in "${declared_packages[@]}"; do
    [[ ${actual_package[$package_file_name]+present} == present ]] ||
        fail "missing package file declared by $changes_file: $package_file_name"
done

declare -A package_file
declare -A package_version
runtime_count=0
dbgsym_count=0
dbgsym_ddeb_count=0
artifact_version=

read_field() {
    set -e

    local field=$1
    local package_file_path=$2
    dpkg-deb --field "$package_file_path" "$field"
}

for package_file_name in "${declared_packages[@]}"; do
    package_file_path=${actual_package[$package_file_name]}
    if ! package_name=$(read_field Package "$package_file_path"); then
        fail "could not read Package from $package_file_path"
    fi
    if ! version=$(read_field Version "$package_file_path"); then
        fail "could not read Version from $package_file_path"
    fi
    [[ -n $package_name ]] || fail "empty Package in $package_file_path"
    [[ -n $version ]] || fail "empty Version in $package_file_path"

    if [[ ${package_file[$package_name]+present} ]]; then
        fail "duplicate package name: $package_name"
    fi
    package_file[$package_name]=$package_file_path
    package_version[$package_name]=$version

    if [[ -z $artifact_version ]]; then
        artifact_version=$version
    elif [[ $version != "$artifact_version" ]]; then
        fail "mixed package versions: $artifact_version and $version"
    fi

    if [[ $package_name == *-dbgsym ]]; then
        ((dbgsym_count += 1))
        if [[ $package_file_path == *.ddeb ]]; then
            ((dbgsym_ddeb_count += 1))
        fi
    else
        ((runtime_count += 1))
    fi
done

[[ $artifact_version == "$changes_version" ]] ||
    fail "package version $artifact_version does not match $changes_file version $changes_version"
(( runtime_count > 0 )) || fail "no ordinary packages found"
(( dbgsym_count > 0 )) || fail "no dbgsym packages found"
(( dbgsym_ddeb_count > 0 )) || fail "no dbgsym .ddeb files found"

for package_name in "${!package_file[@]}"; do
    [[ $package_name == *-dbgsym ]] || continue

    runtime_name=${package_name%-dbgsym}
    if [[ ${package_file[$runtime_name]+present} != present ]]; then
        fail "missing runtime package $runtime_name for $package_name"
    fi

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
