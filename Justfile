#!/usr/bin/env just --justfile

TAG := "yanet2-dev"
ROOT_DIR                := justfile_directory()

default:
  @just --list

all:
	@meson compile -C build

test *IGN: all
	@meson test -C build --print-errorlogs

covclean:
	find build -type f -iname '*.gcda' -delete

coverage:
	@ninja -C build coverage-html

setup:
	@meson setup build -Dbuildtype=debug -Db_coverage=true

dbuild-cnt: ## Собрать докер-образ.
		cd .github/workflows && BUILDKIT_PROGRESS=plain DOCKER_BUILDKIT=1 docker build --platform linux/amd64 \
		-f Dockerfile.base.dev -t {{ TAG }} .

dtest:
		@docker run -it --rm --privileged \
				-v {{ ROOT_DIR }}:/yanet2 \
				-v ~/go:/root/go \
				{{ TAG }} \
				sh -c 'cd /yanet2 && just setup test'
dbuild *IGN:
		@docker run -it --rm \
				-v {{ ROOT_DIR }}:/yanet2 \
				-v ~/go:/root/go \
				{{ TAG }} \
				sh -c 'cd /yanet2 && just setup all'
dshell:
		@docker run -it --rm \
				-v {{ ROOT_DIR }}:/yanet2 \
				-v ~/go:/root/go \
				{{ TAG }} bash
drun *CMDS:
		@docker run -it --rm \
				-v {{ ROOT_DIR }}:/yanet2 \
				-v ~/go:/root/go \
				{{ TAG }} sh -c 'cd /yanet2 && {{ CMDS }}'

dcoverage:
		@docker run -it --rm --privileged \
				-v {{ ROOT_DIR }}:/yanet2 \
				-v {{ ROOT_DIR }}/gocache:/root/go \
				{{ TAG }} \
				sh -c 'cd /yanet2 && just covclean test; just coverage'
