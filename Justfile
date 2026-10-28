#!/usr/bin/env just --justfile

set positional-arguments := true

# Project configuration
export TAG := env_var_or_default("TAG", "yanet2-dev")
ROOT_DIR := justfile_directory()
export DOCKER_CACHE_DIR := env_var_or_default("DOCKER_CACHE_DIR", "/tmp")


# Show available commands
default:
    @just --list

# === Build Targets ===
#
# The build, build-debug and fuzz recipes call the make targets, which also
# pass the private modules to meson. Separate make runs keep setup before the
# build even under a parallel MAKEFLAGS.

# Build all targets
build:
    @make setup
    @make build

# Build all targets in debug with ASan and UBSan
build-debug:
    @make setup-asan
    @make build

# Setup build environment with optional coverage
setup COVERAGE_MODE="false":
    @make setup MESON_SETUP_FLAGS="-Dbuildtype=debug -Db_sanitize=none -Db_coverage={{ COVERAGE_MODE }}"

# === Test Targets ===

# Run tests with optional arguments
test: build
    @meson test -C build --print-errorlogs

# Run functional tests
test-functional: build
    @echo "Running functional tests..."
    @cd tests/functional && make test

# Run all tests (unit + functional)
test-all: test test-functional

# Operate the reusable local QEMU lab.
# Examples: just lab doctor; just lab up; just lab scenario run forward-route
lab *ARGS:
    @go run ./cmd/yanet-lab "$@"

# Build fuzzing targets and optionally run a fuzzer
# Usage: just fuzz [MODULE]
# Example: just fuzz dscp
fuzz MODULE="":
    @make fuzz MODULE={{ MODULE }}

# Clean coverage data
# clean *.gcno file manually after remove c file
covclean:
    @find build -type f -iname '*.gcda' -delete

# Generate coverage report
coverage: covclean test
    @ninja -C build coverage-html

# === Code Quality Targets ===

# Run clang-tidy on specified files
tidy *FILES:
    #!/usr/bin/env bash
    if [ -z "{{ FILES }}" ]; then
        echo "Error: No files specified"
        exit 1
    fi
    clang-tidy-19 -p build --format-style=file {{ FILES }}

# Format code with clang-format
bloody *FILES:
    #!/usr/bin/env bash
    if [ -z "{{ FILES }}" ]; then
        echo "Error: No files specified"
        exit 1
    fi
    clang-format-19 --style=file -i {{ FILES }}

# === Docker Targets ===

# Build development Docker image
dbuild-cnt:
    #!/usr/bin/env bash
    set -euo pipefail
    cd .github/workflows && \
    BUILDKIT_PROGRESS=plain DOCKER_BUILDKIT=1 \
    docker build \
        --platform linux/amd64 \
        -f Dockerfile.base.dev \
        --build-arg BUILDKIT_INLINE_CACHE=1 \
        --cache-from {{ TAG }} \
        -t {{ TAG }} .

# Common Docker run configuration
_docker_run IT *COMMAND:
    #!/usr/bin/env bash

    # Since version 2.35.2 git checks ownership of files in repository
    # and fails if it doesn't match current user. This check triggers
    # inside container since commands are executed as 'root'. And it strikes
    # even if you don't use git directly, 'go test' starts to fails with:
    #    error obtaining VCS status: exit status 128
    #        Use -buildvcs=false to disable VCS stamping.
    # Add working directory to safe list to avoid it.

    set -euo pipefail
    mkdir -p "${DOCKER_CACHE_DIR}/gomodcache" "${DOCKER_CACHE_DIR}/gocache" "${DOCKER_CACHE_DIR}/ccache"
    docker_mounts=(-v "{{ ROOT_DIR }}:/yanet2")
    if [ "{{ ROOT_DIR }}" != /yanet2 ]; then
        docker_mounts+=(-v "{{ ROOT_DIR }}:{{ ROOT_DIR }}")
    fi
    if [ -f "{{ ROOT_DIR }}/.git" ]; then
        # Linked worktrees reference Git metadata outside the source mount.
        git_common_dir="$(git -C "{{ ROOT_DIR }}" rev-parse --path-format=absolute --git-common-dir)"
        docker_mounts+=(-v "${git_common_dir}:${git_common_dir}:ro")
        # Relative gitfiles must remain resolvable after relocation to /yanet2.
        git_dir="$(git -C "{{ ROOT_DIR }}" rev-parse --absolute-git-dir)"
        gitfile="$(mktemp)"
        trap 'rm -f "$gitfile"' EXIT
        printf 'gitdir: %s\n' "$git_dir" > "$gitfile"
        docker_mounts+=(-v "${gitfile}:/yanet2/.git:ro")
    fi
    # Cache entries written as root must stay manageable by the invoking host
    # user.
    #
    # ccache applies this umask only when creating entries in its cache
    # directory and restores the umask before running the compiler, so
    # compile outputs keep default permissions.
    docker run {{ IT }} --rm \
        --platform linux/amd64 \
        --network=host \
        "${docker_mounts[@]}" \
        -v "${DOCKER_CACHE_DIR}/gomodcache:/tmp/gomodcache:rw" \
        -v "${DOCKER_CACHE_DIR}/gocache:/tmp/gocache:rw" \
        -v "${DOCKER_CACHE_DIR}/ccache:/ccache:rw" \
        {{ TAG }} \
        sh -c 'cd "$1" && git config --global --add safe.directory /yanet2 && git config --global --add safe.directory "$1" && export CCACHE_UMASK=000 && shift && sh -c "$@"' sh "{{ ROOT_DIR }}" "${@:2}"

dsetup COVERAGE_MODE="false":
    @just _docker_run -it "just setup {{ COVERAGE_MODE }}"

# Smoke-check that disposable containers share the host compiler cache through
# the shared Docker runner; guards the cache bind mount before the suite runs
dtest-ccache-probe:
    @bash "{{ justfile_directory() }}/scripts/verify-docker-ccache.sh" "{{ justfile() }}"

_ccache_setup *OPTIONS:
    @test ! -f build/build.ninja || python3 scripts/check-meson-ccache.py build
    @meson setup build {{ OPTIONS }}
    @python3 scripts/check-meson-ccache.py build

# Options select only Go tests; successful runs retain the C build and Meson suite.
dtest GO_TEST_FLAGS="" GO_TEST_PACKAGES="": dtest-ccache-probe
    #!/usr/bin/env bash
    set -euo pipefail
    just _docker_run -it 'python3 scripts/test-meson-ccache.py && make test-recipes && { test ! -f build/build.ninja || python3 scripts/check-meson-ccache.py build; } && just setup false && python3 scripts/check-meson-ccache.py build && make build && GO_TEST_FLAGS="$1" GO_TEST_PACKAGES="$2" make test' sh "$@"

# Run clang-tidy in Docker
dtidy *FILES:
    @just _docker_run -q "just tidy {{ FILES }}"

# Run clang-format in Docker
dbloody *FILES:
    @just _docker_run -q "just bloody {{ FILES }}"

# Build in Docker
dbuild:
    @just _docker_run -it "just _ccache_setup && meson compile -C build"

# Build in Docker
dbuild-debug:
    @just _docker_run -it "just build-debug"

# Start shell in Docker
dshell:
    @just _docker_run -it "bash"

# Run arbitrary commands in Docker
drun *CMDS:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -z "{{ CMDS }}" ]; then
        echo "Error: No commands specified"
        exit 1
    fi
    just _docker_run -it "{{ CMDS }}"

# Generate coverage report in Docker
dcoverage:
    @just _docker_run -q "just setup true && just coverage"

# Build controlplane in Docker
dcontrolplane:
    @just _docker_run -q "make controlplane"

# Build CLI in Docker
dcli:
    @just _docker_run -q "make cli"

# Build deb packages for all main components inside Docker container (unified with CI)
ddeb:
    @just _docker_run -q "./scripts/build-deb.sh"

# Run fuzzing in Docker
# Usage: just dfuzz [MODULE]
dfuzz MODULE="":
    @just _docker_run -it "just fuzz {{ MODULE }}"
