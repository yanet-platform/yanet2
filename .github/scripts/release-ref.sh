#!/bin/bash
set -euo pipefail

fail() {
    echo "release preflight failed: $*" >&2
    exit 1
}

valid_version() {
    [[ $1 =~ ^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$ ]]
}

# Whether the first commit is on the first-parent history of the second.
#
# grep consumes the whole list, so a history longer than a pipe buffer never
# ends the listing with a broken pipe under pipefail.
on_first_parent() {
    git rev-list --first-parent "$2" | grep -Fx "$1" >/dev/null
}

check_source() {
    local sha=$1 branch=$2
    [[ $sha =~ ^[0-9a-f]{40}$ ]] || fail "invalid commit SHA: $sha"
    [[ $(git rev-parse HEAD) == "$sha" ]] || fail "checkout does not match $sha"
    git show-ref --verify --quiet "refs/remotes/origin/$branch" ||
        fail "release branch is missing: $branch"
    on_first_parent "$sha" "refs/remotes/origin/$branch" ||
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

# Creates or fast-forwards the branch of a stable release to its tag.
#
# A patch release needs its previous release published, and every newer tag
# of the release line must hold this tag's commit on its first-parent
# history. A missing branch starts at the tag when the tag is on main's
# first-parent history (x.y.0) or holds the previous release on its own. An
# existing branch that already holds the tag stays; otherwise it only moves
# to a tag on main whose first-parent history holds the branch tip and the
# previous release.
prepare_branch() {
    local major=$1 minor=$2 patch=$3 sha=$4
    local branch="release/$major.$minor" previous="" draft tag newer_sha branch_sha previous_sha

    if ((patch > 0)); then
        previous="v$major.$minor.$((patch - 1))"
        draft=$(gh release view "$previous" --json isDraft --jq '.isDraft')
        [[ $draft == false ]] || fail "previous release is not published: $previous"
    fi
    while IFS= read -r tag; do
        [[ $tag =~ ^v${major}\.${minor}\.(0|[1-9][0-9]{0,8})$ ]] || continue
        ((BASH_REMATCH[1] > patch)) || continue
        newer_sha=$(git rev-parse "refs/tags/$tag^{commit}")
        on_first_parent "$sha" "$newer_sha" ||
            fail "newer release tag does not descend from v$major.$minor.$patch: $tag"
    done < <(git tag --list "v$major.$minor.*")

    if git show-ref --verify --quiet "refs/remotes/origin/$branch"; then
        on_first_parent "$sha" "refs/remotes/origin/$branch" && return 0
        on_first_parent "$sha" refs/remotes/origin/main ||
            fail "$sha is not on main's first-parent history"
        branch_sha=$(git rev-parse "refs/remotes/origin/$branch")
        on_first_parent "$branch_sha" "$sha" ||
            fail "$branch is not on $sha's first-parent history"
    elif ((patch == 0)); then
        on_first_parent "$sha" refs/remotes/origin/main ||
            fail "$sha is not on main's first-parent history"
    fi
    if ((patch > 0)); then
        previous_sha=$(git rev-parse "refs/tags/$previous^{commit}")
        on_first_parent "$previous_sha" "$sha" ||
            fail "$sha does not descend from $previous on its first-parent history"
    fi
    # Git reports to stderr, so stdout carries only the step outputs.
    git push origin "$sha:refs/heads/$branch" >&2
    git fetch origin "refs/heads/$branch:refs/remotes/origin/$branch" >&2
}

[[ $# -ge 1 ]] || fail "expected candidate or prepare mode"
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
    prepare)
        [[ $# -eq 3 ]] || fail "usage: $0 prepare <tag ref> <run-id> <attempt>"
        ref=$1 run_id=$2 attempt=$3
        [[ $ref =~ ^refs/tags/v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$ ]] ||
            fail "invalid release tag: $ref"
        major=${BASH_REMATCH[1]} minor=${BASH_REMATCH[2]} patch=${BASH_REMATCH[3]}
        [[ $run_id =~ ^[1-9][0-9]*$ && $attempt =~ ^[1-9][0-9]{0,2}$ ]] ||
            fail "invalid release run identifier"
        version="$major.$minor.$patch"
        branch="release/$major.$minor"
        tag="v$version"
        sha=$(git rev-parse "$ref^{commit}")
        prepare_branch "$major" "$minor" "$patch" "$sha"
        check_source "$sha" "$branch"
        git show-ref --verify --quiet "refs/tags/$tag" || fail "tag is missing: $tag"
        [[ $(git rev-parse "refs/tags/$tag^{commit}") == "$sha" ]] ||
            fail "tag $tag does not point to $sha"
        check_patch_order "$version" "$sha" "$tag"
        printf 'source_sha=%s\npackage_version=%s\nimage_tag=%s\n' \
            "$sha" "$version" "$version"
        printf 'stage_tag=%s-stage-%s-%s\nrelease_tag=%s\n' \
            "$version" "$run_id" "$attempt" "$tag"
        ;;
    *) fail "unknown mode: $mode" ;;
esac
