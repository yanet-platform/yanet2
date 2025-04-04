#!/usr/bin/env just --justfile

# Project configuration
TAG := "yanet2-dev"
ROOT_DIR := justfile_directory()

# Show available commands
default:
    @just --list

# Build all targets
all:
    @meson compile -C build

# Run tests with optional arguments
test *IGN: all
    @meson test -C build --print-errorlogs

# Clean coverage data
covclean:
    @find build -type f -iname '*.gcda' -delete

# Generate coverage report
coverage:
    @ninja -C build coverage-html

# Setup build environment
setup:
    @meson setup build -Dbuildtype=debug -Db_coverage=false

# Build development Docker image
dbuild-cnt: ## Собрать докер-образ
    #!/usr/bin/env bash
    set -euo pipefail
    cd .github/workflows && \
    BUILDKIT_PROGRESS=plain DOCKER_BUILDKIT=1 \
    docker build \
        --platform linux/amd64 \
        -f Dockerfile.base.dev \
        -t {{ TAG }} .

# Run tests in Docker
dtest:
    #!/usr/bin/env bash
    set -euo pipefail
    docker run -it --rm --privileged \
        -v {{ ROOT_DIR }}:/yanet2 \
        -v /tmp/gomodcache:/tmp/gomodcache:rw \
        -v /tmp/gocache:/tmp/gocache:rw \
        {{ TAG }} \
        sh -c 'cd /yanet2 && just setup test'

# Build in Docker
dbuild *IGN:
    #!/usr/bin/env bash
    set -euo pipefail
    docker run -it --rm \
        -v {{ ROOT_DIR }}:/yanet2 \
        -v /tmp/gomodcache:/tmp/gomodcache:rw \
        -v /tmp/gocache:/tmp/gocache:rw \
        {{ TAG }} \
        sh -c 'cd /yanet2 && just setup all'

# Start shell in Docker
dshell:
    #!/usr/bin/env bash
    set -euo pipefail
    docker run -it --rm \
        -v {{ ROOT_DIR }}:/yanet2 \
        -v /tmp/gomodcache:/tmp/gomodcache:rw \
        -v /tmp/gocache:/tmp/gocache:rw \
        {{ TAG }} bash

# Run commands in Docker
drun *CMDS:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -z "{{ CMDS }}" ]; then
        echo "Error: No commands specified"
        exit 1
    fi
    docker run -it --rm \
        -v {{ ROOT_DIR }}:/yanet2 \
        -v /tmp/gomodcache:/tmp/gomodcache:rw \
        -v /tmp/gocache:/tmp/gocache:rw \
        {{ TAG }} \
        sh -c 'cd /yanet2 && {{ CMDS }}'

# Generate coverage report in Docker
dcoverage:
    #!/usr/bin/env bash
    set -euo pipefail
    docker run -it --rm --privileged \
        -v {{ ROOT_DIR }}:/yanet2 \
        -v /tmp/gomodcache:/tmp/gomodcache:rw \
        -v /tmp/gocache:/tmp/gocache:rw \
        {{ TAG }} \
        sh -c 'cd /yanet2 && just covclean test; just coverage'

# Build controlplane in Docker
dcontrolplane:
    #!/usr/bin/env bash
    set -euo pipefail
    docker run -it --rm \
        -v {{ ROOT_DIR }}:/yanet2 \
        -v /tmp/gomodcache:/tmp/gomodcache:rw \
        -v /tmp/gocache:/tmp/gocache:rw \
        {{ TAG }} \
        sh -c 'cd /yanet2/controlplane && make build'
