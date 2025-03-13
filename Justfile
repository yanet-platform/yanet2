#!/usr/bin/env just --justfile

TAG := "yanet2-dev"
ROOT_DIR                := justfile_directory()
OS := `uname -o`

default:
  @just --list

all:
	[ "{{ OS }}" = Darwin ] || meson compile -C build

test *IGN: all
	[ "{{ OS }}" = Darwin ] || meson test -C build --print-errorlogs
	[ "{{ OS }}" != Darwin ] || just dtest

coverage:
	find build -type f -iname '*.gcda' && ninja -C build coverage-html

setup:
	@meson setup build -Dbuildtype=debug -Db_coverage=true

dbuild-cnt: ## Собрать докер-образ.
		cd .github/workflows && BUILDKIT_PROGRESS=plain DOCKER_BUILDKIT=1 docker build --platform linux/amd64 \
		-f Dockerfile.base.dev -t {{ TAG }} .

dtest:
		@docker run -it --rm --privileged \
				-v {{ ROOT_DIR }}:/yanet2 \
				{{ TAG }} \
				sh -c 'cd /yanet2 && just setup test'
dbuild *IGN:
		@docker run -it --rm \
				-v {{ ROOT_DIR }}:/yanet2 \
				{{ TAG }} \
				sh -c 'cd /yanet2 && just setup all'
dshell:
		@docker run -it --rm \
				-v {{ ROOT_DIR }}:/yanet2 \
				{{ TAG }} bash
drun *CMDS:
		@docker run -it --rm \
				-v {{ ROOT_DIR }}:/yanet2 \
				{{ TAG }} sh -c 'cd /yanet2 && {{ CMDS }}'
