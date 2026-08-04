#!/usr/bin/env just --justfile

set positional-arguments := true

# Project configuration
TAG := "yanet2-dev"
ROOT_DIR := justfile_directory()
DOCKER_CACHE_DIR := "/tmp"


# Show available commands
default:
    @just --list

# === Build Targets ===

# Build all targets
build:
    @go clean -cache
    @meson setup build
    @meson compile -C build

# Build all targets in debug
build-debug:
    @go clean -cache
    @meson setup -Dbuildtype=debug -Doptimization=0 -Db_sanitize=address,undefined build
    @meson compile -C build

# Setup build environment with optional coverage
setup COVERAGE_MODE="false":
    @meson setup build -Dbuildtype=debug -Db_coverage={{ COVERAGE_MODE }}

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
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -d build ] && ! meson introspect build --buildoptions | jq -er '.[] | select(.name=="fuzzing") | .value' | grep -q enabled; then
        echo "Wiping build for fuzzing..."
        rm -rf build
    fi
    if [ ! -d build ]; then
        env CC=clang CXX=clang++ meson setup -Dbuildtype=debug -Doptimization=0 -Dfuzzing=enabled build
    fi
    env CC=clang CXX=clang++ meson compile -C build
    echo "Ready to fuzz the following modules:"
    find build/tests/fuzzing/ -type f -executable -printf '%f\n'
    if [ -n "{{ MODULE }}" ]; then
        mkdir -p corpus
        ./build/tests/fuzzing/{{ MODULE }} corpus/
    fi

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
    docker run {{ IT }} --rm \
        --platform linux/amd64 \
        --network=host \
        -v {{ ROOT_DIR }}:/yanet2 \
        -v {{ DOCKER_CACHE_DIR }}/gomodcache:/tmp/gomodcache:rw \
        -v {{ DOCKER_CACHE_DIR }}/gocache:/tmp/gocache:rw \
        {{ TAG }} \
        sh -c 'cd /yanet2 && git config --global --add safe.directory /yanet2 && {{ COMMAND }}'

dsetup COVERAGE_MODE="false":
    @just _docker_run -it "just setup {{ COVERAGE_MODE }}"

# Run tests in Docker
dtest:
    @just _docker_run -it "just setup false test"

# Run clang-tidy in Docker
dtidy *FILES:
    @just _docker_run -q "just tidy {{ FILES }}"

# Run clang-format in Docker
dbloody *FILES:
    @just _docker_run -q "just bloody {{ FILES }}"

# Build in Docker
dbuild:
    @just _docker_run -it "just build"

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
