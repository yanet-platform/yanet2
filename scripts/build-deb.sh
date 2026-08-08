#!/bin/bash
set -euxo pipefail
env

# Resolve the debian package version from git.
#
# A `vX.Y.Z` tag on HEAD becomes `X.Y.Z`, otherwise
# `<base>+dev<count>.g<sha>` where <base> is the latest reachable tag, or,
# when no such tag exists yet, the last released version already recorded
# in debian/changelog (with any leftover `+dev...` suffix stripped, so a
# base can never compound across runs). <count> is the number of commits
# reachable from HEAD, and <sha> is the abbreviated commit hash. Falls back
# to the version already recorded in debian/changelog if git is unavailable
# or any of the git calls fail, so the build never aborts here.
resolve_version() {
    set -e

    if ! git -c safe.directory="$PWD" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        echo "warning: git version resolution failed, falling back to the changelog version" >&2
        dpkg-parsechangelog -SVersion
        return
    fi

    local tag
    if tag=$(git -c safe.directory="$PWD" describe --tags --exact-match --match 'v[0-9]*' HEAD 2>/dev/null); then
        echo "${tag#v}"
        return
    fi

    local base
    if ! base=$(git -c safe.directory="$PWD" describe --tags --abbrev=0 --match 'v[0-9]*' HEAD 2>/dev/null); then
        if ! base=$(dpkg-parsechangelog -SVersion 2>/dev/null); then
            base="v0.0.0"
        fi
        base="${base%%+dev*}"
    fi

    local count sha
    if ! count=$(git -c safe.directory="$PWD" rev-list --count HEAD); then
        echo "warning: git version resolution failed, falling back to the changelog version" >&2
        dpkg-parsechangelog -SVersion
        return
    fi
    if ! sha=$(git -c safe.directory="$PWD" rev-parse --short=8 HEAD); then
        echo "warning: git version resolution failed, falling back to the changelog version" >&2
        dpkg-parsechangelog -SVersion
        return
    fi

    echo "${base#v}+dev${count}.g${sha}"
}

PKG_VERSION="${PKG_VERSION:-$(resolve_version)}"
echo "Resolved package version: $PKG_VERSION"

# dch needs a maintainer identity; default it when the environment doesn't
# already provide one.
: "${DEBEMAIL:=ci@yanet-platform.dev}"
: "${DEBFULLNAME:=YANET CI}"
export DEBEMAIL DEBFULLNAME

changelog_backup=$(mktemp)
cp debian/changelog "$changelog_backup"
trap 'cp "$changelog_backup" debian/changelog; rm -f "$changelog_backup"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# The resolved version can sort below the version already tracked in
# debian/changelog, so force dch to accept it. The entry text is passed as
# an argument so dch never tries to open an editor.
dch --newversion "$PKG_VERSION" --distribution unstable --force-bad-version "Automatic CI build."

# Install build deps with verbose output
apt-get update
apt-get -o Debug::pkgProblemResolver=yes --no-install-recommends -y --allow-downgrades --allow-remove-essential --allow-change-held-packages build-dep . 2>&1 | tee build-deps.log

echo "Current PATH during build: $PATH" > build-path.log
# Preserve environment variables for cargo (order matters in dpkg-buildpackage)
echo y | debuild --preserve-envvar=PATH -us -uc 2>&1 | tee build.log 

mkdir -p outdeb
dcmd cp ../*.changes outdeb/
