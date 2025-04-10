#!/usr/bin/env just --justfile

# Project configuration
TAG := "yanet2-dev"
ROOT_DIR := justfile_directory()
DOCKER_CACHE_DIR := "/tmp"

# Required tools - used for validation
required_tools := "docker clang-tidy-19 clang-format-19 meson ninja"

# Show available commands
default:
    @just --list

# === Build Targets ===

# Build all targets
all:
    @meson compile -C build

# Setup build environment with optional coverage
setup:
    @meson setup build -Dbuildtype=debug -Db_coverage=false

# === Test Targets ===

# Run tests with optional arguments
test *IGN: all
    @meson test -C build --print-errorlogs

# Clean coverage data
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
_docker_run *COMMAND:
    #!/usr/bin/env bash
    set -euo pipefail
    docker run -it --rm \
        -v {{ ROOT_DIR }}:/yanet2 \
        -v {{ DOCKER_CACHE_DIR }}/gomodcache:/tmp/gomodcache:rw \
        -v {{ DOCKER_CACHE_DIR }}/gocache:/tmp/gocache:rw \
        {{ TAG }} \
        sh -c 'cd /yanet2 && {{ COMMAND }}'

# Run tests in Docker
dtest:
    @just _docker_run "just setup test"

# Run clang-tidy in Docker
dtidy *FILES:
    @just _docker_run "just tidy {{ FILES }}"

# Run clang-format in Docker
dbloody *FILES:
    @just _docker_run "just bloody {{ FILES }}"

# Build in Docker
dbuild *IGN:
    @just _docker_run "just setup all"

# Start shell in Docker
dshell:
    @just _docker_run "bash"

# Run arbitrary commands in Docker
drun *CMDS:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -z "{{ CMDS }}" ]; then
        echo "Error: No commands specified"
        exit 1
    fi
    just _docker_run "{{ CMDS }}"

# Generate coverage report in Docker
dcoverage:
    @just _docker_run "just setup && just coverage"

# Build controlplane in Docker
dcontrolplane:
    @just _docker_run "cd controlplane && make build"
