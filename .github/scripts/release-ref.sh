#!/bin/bash
set -euo pipefail

fail() {
    echo "release preflight failed: $*" >&2
    exit 1
}

valid_version() {
    [[ $1 =~ ^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$ ]]
}

check_source() {
    local sha=$1 branch=$2
    [[ $sha =~ ^[0-9a-f]{40}$ ]] || fail "invalid commit SHA: $sha"
    [[ $(git rev-parse HEAD) == "$sha" ]] || fail "checkout does not match $sha"
    git show-ref --verify --quiet "refs/remotes/origin/$branch" ||
        fail "release branch is missing: $branch"
    git rev-list --first-parent "refs/remotes/origin/$branch" |
        grep -Fx "$sha" >/dev/null ||
        fail "$sha is not on $branch's first-parent history"
}

check_patch_order() {
    local version=$1 sha=$2 current_tag=${3:-}
    local major minor patch previous previous_sha tag tagged_patch
    IFS=. read -r major minor patch <<<"$version"

    if ((patch > 0)); then
        previous="v$major.$minor.$((patch - 1))"
        git show-ref --verify --quiet "refs/tags/$previous" ||
            fail "previous release tag is missing: $previous"
        previous_sha=$(git rev-parse "refs/tags/$previous^{commit}")
        git merge-base --is-ancestor "$previous_sha" "$sha" ||
            fail "$version is not a descendant of $previous"
    fi

    [[ -n $current_tag ]] && return 0
    while IFS= read -r tag; do
        [[ $tag =~ ^v${major}\.${minor}\.(0|[1-9][0-9]{0,8})$ ]] || continue
        tagged_patch=${BASH_REMATCH[1]}
        ((tagged_patch < patch)) || fail "non-increasing release tag: $tag"
    done < <(git tag --list "v$major.$minor.*")
}

[[ $# -ge 1 ]] || fail "expected candidate or stable mode"
mode=$1
shift
case $mode in
    candidate)
        [[ $# -eq 5 ]] || fail "usage: $0 candidate <ref> <version> <sha> <run-id> <attempt>"
        ref=$1 version=$2 sha=$3 run_id=$4 attempt=$5
        [[ $ref =~ ^refs/heads/release/(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$ ]] ||
            fail "invalid release branch: $ref"
        branch_major=${BASH_REMATCH[1]} branch_minor=${BASH_REMATCH[2]}
        branch=${ref#refs/heads/}
        valid_version "$version" || fail "invalid release version: $version"
        [[ $version == "$branch_major.$branch_minor."* ]] ||
            fail "version $version does not match $branch"
        [[ $run_id =~ ^[1-9][0-9]*$ && $attempt =~ ^[1-9][0-9]{0,2}$ ]] ||
            fail "invalid candidate run identifier"
        check_source "$sha" "$branch"
        git show-ref --verify --quiet "refs/tags/v$version" &&
            fail "release tag already exists: v$version"
        check_patch_order "$version" "$sha"
        printf -v attempt_suffix '%03d' "$attempt"
        candidate_number="$run_id$attempt_suffix"
        printf 'source_sha=%s\npackage_version=%s~rc%s\nimage_tag=%s-rc%s\n' \
            "$sha" "$version" "$candidate_number" "$version" "$candidate_number"
        ;;
    stable)
        [[ $# -eq 2 ]] || fail "usage: $0 stable <ref> <sha>"
        ref=$1 sha=$2
        [[ $ref =~ ^refs/tags/v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$ ]] ||
            fail "invalid release tag: $ref"
        major=${BASH_REMATCH[1]} minor=${BASH_REMATCH[2]} patch=${BASH_REMATCH[3]}
        version="$major.$minor.$patch"
        branch="release/$major.$minor"
        tag="v$version"
        check_source "$sha" "$branch"
        git show-ref --verify --quiet "refs/tags/$tag" || fail "tag is missing: $tag"
        [[ $(git rev-parse "refs/tags/$tag^{commit}") == "$sha" ]] ||
            fail "tag $tag does not point to $sha"
        check_patch_order "$version" "$sha" "$tag"
        printf 'source_sha=%s\npackage_version=%s\nimage_tag=%s\n' \
            "$sha" "$version" "$version"
        ;;
    *) fail "unknown mode: $mode" ;;
esac
