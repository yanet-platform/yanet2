#!/bin/bash
set -euo pipefail

export LC_ALL=C

repo_root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
helper=$repo_root/.github/scripts/release-ref.sh
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

fail() {
    echo "release helper test failed: $*" >&2
    exit 1
}

# A gh stand-in that answers release views from files.
#
# $RELEASES/<tag> holds "false" for a published release and "true" for a
# draft; a missing file is a release that does not exist.
mkdir -p "$test_root/bin" "$test_root/releases"
cat >"$test_root/bin/gh" <<'GH'
#!/bin/bash
set -euo pipefail
[[ $1 == release && $2 == view ]] || exit 2
[[ -f $RELEASES/$3 ]] || { echo "release not found: $3" >&2; exit 1; }
cat "$RELEASES/$3"
GH
chmod +x "$test_root/bin/gh"
export PATH="$test_root/bin:$PATH" RELEASES="$test_root/releases"
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com

git init -q --bare -b main "$test_root/origin.git"
git clone -q "$test_root/origin.git" "$test_root/work" 2>/dev/null
cd "$test_root/work"
git switch -q -c main

commit() {
    git commit -q --allow-empty -m "$1"
}

# Tags the current commit the way releases are tagged, annotated, and pushes
# the current branch and the tag; the release starts published.
release_tag() {
    git tag -a -m "$1" "$1"
    git push -q origin HEAD "refs/tags/$1"
    git fetch -q origin
    echo false >"$RELEASES/$1"
}

remote_branch() {
    git ls-remote origin "refs/heads/$1" | cut -f1
}

tag_commit() {
    git rev-parse "refs/tags/$1^{commit}"
}

# Runs a helper mode on a detached checkout, like the release workflows.
#
# The step outputs land in $out, the diagnostics in $err and the exit status
# in $status.
run_helper() {
    local checkout=$1
    shift
    git switch -q --detach "$checkout"
    status=0
    out=$(bash "$helper" "$@" 2>"$test_root/err") || status=$?
    err=$(cat "$test_root/err")
    git switch -q main
}

prepare() {
    run_helper "refs/tags/$1" prepare "refs/tags/$1" 7 1
}

expect_ok() {
    [[ $status -eq 0 ]] || fail "$1: exit $status: $err"
}

expect_fail() {
    [[ $status -ne 0 && $err == *"$2"* ]] || fail "$1: expected a failure with '$2', got exit $status: $err"
}

# The stable outputs, exactly and nothing else.
expect_outputs() {
    local version=$1
    local want
    want=$(printf 'source_sha=%s\npackage_version=%s\nimage_tag=%s\nstage_tag=%s-stage-7-1\nrelease_tag=v%s' \
        "$(tag_commit "v$version")" "$version" "$version" "$version" "$version")
    [[ $out == "$want" ]] || fail "outputs of v$version: $out"
}

commit initial

# Release line 1.2: open, rerun, refuse a draft predecessor, fast-forward,
# backport on the branch, then refuse tags the branch cannot reach.
commit feature
release_tag v1.2.0
prepare v1.2.0
expect_ok "x.y.0 from main"
expect_outputs 1.2.0
[[ $(remote_branch release/1.2) == "$(tag_commit v1.2.0)" ]] || fail "release/1.2 was not created at v1.2.0"

prepare v1.2.0
expect_ok "rerun of a prepared release"
expect_outputs 1.2.0

commit fix
release_tag v1.2.1
echo true >"$RELEASES/v1.2.0"
prepare v1.2.1
expect_fail "draft previous release" "previous release is not published: v1.2.0"
[[ $(remote_branch release/1.2) == "$(tag_commit v1.2.0)" ]] || fail "a refused release moved release/1.2"

echo false >"$RELEASES/v1.2.0"
prepare v1.2.1
expect_ok "patch release from main"
expect_outputs 1.2.1
[[ $(remote_branch release/1.2) == "$(tag_commit v1.2.1)" ]] || fail "release/1.2 was not fast-forwarded to v1.2.1"

git switch -q -c backport origin/release/1.2
commit "backported fix"
git push -q origin HEAD:refs/heads/release/1.2
release_tag v1.2.2
git switch -q main
prepare v1.2.2
expect_ok "backport tagged on the branch"
[[ $(remote_branch release/1.2) == "$(tag_commit v1.2.2)" ]] || fail "a backport tag moved release/1.2"

commit "more on main"
release_tag v1.2.3
prepare v1.2.3
expect_fail "tag on main past a backport" "release/1.2 is not on"
[[ $(remote_branch release/1.2) == "$(tag_commit v1.2.2)" ]] || fail "release/1.2 moved off its backport"

git switch -q -c side "$(tag_commit v1.2.2)"
commit "side fix"
release_tag v1.2.4
git switch -q main
prepare v1.2.4
expect_fail "patch on a side line" "is not on main's first-parent history"
[[ $(remote_branch release/1.2) == "$(tag_commit v1.2.2)" ]] || fail "a side-line tag moved release/1.2"

# Release line 1.3: an x.y.0 tag off main opens nothing.
git switch -q -c side13 main
commit "side change"
release_tag v1.3.0
git switch -q main
prepare v1.3.0
expect_fail "x.y.0 off main" "is not on main's first-parent history"
[[ -z $(remote_branch release/1.3) ]] || fail "release/1.3 was created for a tag off main"

# Release line 1.4: a newer tag that does not hold this one refuses it.
commit "line 1.4"
release_tag v1.4.0
commit "after 1.4.0"
git switch -q -c stale14 "$(tag_commit v1.4.0)"
commit "stale patch"
release_tag v1.4.1
git switch -q main
release_tag v1.4.2
prepare v1.4.1
expect_fail "newer tag off the release" "newer release tag does not descend from v1.4.1: v1.4.2"

# Release line 1.5: with no branch, a patch tag needs the previous release
# on its first-parent history.
commit "before 1.5.0"
git tag -a -m v1.5.1 v1.5.1
git push -q origin main refs/tags/v1.5.1
commit "line 1.5"
release_tag v1.5.0
prepare v1.5.1
expect_fail "patch below its predecessor" "does not descend from v1.5.0 on its first-parent history"
[[ -z $(remote_branch release/1.5) ]] || fail "release/1.5 was created for a patch below v1.5.0"

# Release line 1.6: a missing branch is recreated from a patch off main.
commit "line 1.6"
release_tag v1.6.0
git switch -q -c side16
commit "fix for 1.6"
release_tag v1.6.1
git switch -q main
prepare v1.6.1
expect_ok "branch recreated from a patch off main"
[[ $(remote_branch release/1.6) == "$(tag_commit v1.6.1)" ]] || fail "release/1.6 was not created at v1.6.1"

run_helper origin/release/1.6 candidate refs/heads/release/1.6 1.6.2 "$(tag_commit v1.6.1)" 7 1
expect_ok "candidate"
grep -Fx "package_version=1.6.2~rc7001" <<<"$out" >/dev/null || fail "candidate outputs: $out"

run_helper main prepare refs/tags/v01.2.0 7 1
expect_fail "malformed tag" "invalid release tag: refs/tags/v01.2.0"

# A first-parent history longer than a pipe buffer: 3000 commits.
{
    parent=$(git rev-parse main)
    for i in $(seq 1 3000); do
        printf 'commit refs/heads/main\ncommitter test <test@example.com> %d +0000\ndata 2\n%s\n' "$((1700000000 + i))" x
        [[ $i -eq 1 ]] && printf 'from %s\n' "$parent"
        printf '\n'
    done
} | git fast-import --quiet
git reset -q --hard main
release_tag v2.0.0
prepare v2.0.0
expect_ok "x.y.0 at the tip of a long history"
expect_outputs 2.0.0

echo "release helper tests passed"
