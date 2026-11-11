#!/bin/bash
set -euo pipefail

fail() {
    echo "release image promotion failed: $*" >&2
    exit 1
}

[[ $# -eq 3 ]] || fail "usage: $0 <stage-tag> <release-version> <metadata-file>"
stage_tag=$1
release_version=$2
metadata_file=$3
[[ $release_version =~ ^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$ ]] ||
    fail "invalid release version: $release_version"
[[ $stage_tag == "$release_version-stage-"* ]] ||
    fail "invalid staging tag: $stage_tag"
stage_suffix=${stage_tag#"$release_version-stage-"}
[[ $stage_suffix =~ ^[1-9][0-9]*-[1-9][0-9]*$ ]] ||
    fail "invalid staging tag: $stage_tag"

crane_bin=${CRANE_BIN:-$(go env GOPATH)/bin/crane}
[[ -x $crane_bin ]] || fail "registry copy tool is not executable: $crane_bin"

: >"$metadata_file"
for component in dataplane controlplane bird-adapter generic-operator pipeline-operator route-operator neighbour-sidecar; do
    image="ghcr.io/yanet-platform/yanet2/$component"
    stage_digest=$("$crane_bin" digest "$image:$stage_tag") ||
        fail "could not inspect staged image: $image:$stage_tag"
    tags=$("$crane_bin" ls "$image") || fail "could not list image tags: $image"
    if grep -Fxq -- "$release_version" <<<"$tags"; then
        final_digest=$("$crane_bin" digest "$image:$release_version") ||
            fail "could not inspect existing image: $image:$release_version"
        [[ $final_digest == "$stage_digest" ]] ||
            fail "refusing to replace $image:$release_version with a different digest"
    else
        "$crane_bin" tag "$image:$stage_tag" "$release_version" ||
            fail "could not promote $image:$stage_tag"
        final_digest=$("$crane_bin" digest "$image:$release_version") ||
            fail "could not inspect promoted image: $image:$release_version"
        [[ $final_digest == "$stage_digest" ]] ||
            fail "promoted digest differs from staging: $image:$release_version"
    fi
    manifest_size=$("$crane_bin" manifest "$image@$final_digest" | wc -c) ||
        fail "could not inspect manifest size: $image@$final_digest"
    printf '%s:%s\t%s\t%s\n' \
        "$image" "$release_version" "$final_digest" "$manifest_size" >>"$metadata_file"
done
