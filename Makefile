CARGO ?= cargo
RULESYNC ?= npx --yes rulesync@16.8.0
RULESYNC_GENERATE_ARGS := generate --targets claudecode,codexcli,opencode --features subagents

# Compile database consumed by lint/clang-syntax.
COMPILE_DB ?= build/compile_commands.json
RTE_CONFIG ?= build/subprojects/dpdk/rte_build_config.h
YANET_CACHE_LINE_CPPFLAG = -DYANET_CACHE_LINE_SIZE=$$(awk '$$2 == "RTE_CACHE_LINE_SIZE" { print $$3; exit }' "$(RTE_CONFIG)")

# Default PREFIX for debian packaging
PREFIX ?= /usr
BINDIR ?= $(PREFIX)/bin

TARGET_DIR ?= target
RELEASE_DIR := $(TARGET_DIR)/release

# Core CLI packages/binaries that live in cli workspace.
CLI_CORE_MODULES := \
	common \
	device-list \
	inspect \
	pipeline \
	function \
	counters \
	gateway \
	ready \
	metrics

CLI_CORE_BINARIES := yanet-cli $(addprefix yanet-cli-,$(CLI_CORE_MODULES))

# Module CLI packages/binaries.
#
# These are modules that historically had modules/<module>/cli/Makefile.
# If a new module CLI appears, add its module name here.
CLI_MODULES := \
	acl \
	balancer2 \
	blackhole \
	decap \
	device-plain \
	device-vlan \
	device-trafgen \
	dscp \
	fwstate \
	fwstatemap \
	mirror \
	route \
	route-mpls \
	forward \
	nat64 \
	pdump \
	unrdup \
	operator-neighbour \
	operator-pipeline \
	operator-route

# Public module directories, whitelisted in .gitignore under /modules/*.
#
# Anything under modules/ that is not listed here is a private, gitignored
# module (see modules/meson.build's extra_modules option).
PUBLIC_MODULES := \
	acl \
	balancer2 \
	blackhole \
	decap \
	dscp \
	forward \
	fwstate \
	mirror \
	nat64 \
	pdump \
	route \
	route-mpls \
	unrdup

# All directories under modules/, public and private alike.
MODULE_DIRS := $(notdir $(patsubst %/,%,$(wildcard modules/*/)))

# Private module directories not in the public whitelist.
EXTRA_MODULES := $(filter-out $(PUBLIC_MODULES),$(MODULE_DIRS))

# Private modules that ship a CLI crate.
EXTRA_CLI_MODULES := $(foreach m,$(EXTRA_MODULES),$(if $(wildcard modules/$(m)/cli/Cargo.toml),$(m)))

CLI_MODULES += $(EXTRA_CLI_MODULES)

EMPTY :=
SPACE := $(EMPTY) $(EMPTY)
COMMA := ,

# -Dextra_modules= flag for `meson setup`/`meson configure`, comma-joining
# the private module directories.
#
# Always emitted, even as a bare -Dextra_modules= on a clean public tree,
# so that reconfiguring an existing build dir clears the cached array once
# the last private module is removed.
EXTRA_MODULES_FLAG := -Dextra_modules=$(subst $(SPACE),$(COMMA),$(EXTRA_MODULES))

CLI_MODULE_BINARIES := $(addprefix yanet-cli-,$(CLI_MODULES))

# Everything we install for CLI.
#
# Use sort to avoid duplicate binaries because some module binaries are also
# present in CLI_CORE_BINARIES.
CLI_BINARIES := $(sort $(CLI_CORE_BINARIES) $(CLI_MODULE_BINARIES))

# Full paths to built release binaries.
CLI_RELEASE_BINARIES := $(addprefix $(RELEASE_DIR)/,$(CLI_BINARIES))

.PHONY: \
	all \
	setup \
	setup-debug \
	setup-asan \
	dataplane \
	install \
	install1 \
	clean \
	go-cache-clean \
	proto-lint \
	proto-breaking \
	proto-go \
	lint-go \
	lint/clang-syntax \
	lint-commit \
	ai/agents \
	lint/agents \
	hooks \
	test \
	test-only \
	test-asan \
	test-asan-only \
	test-tsan \
	test-functional \
	bench \
	fuzz \
	cli \
	cli-build \
	cli-install \
	cli-core-install \
	cli-clean

# cli/% is a chained prerequisite of cli-install/%.
#
# Mark it precious so make does not treat its recipe output as a deletable
# intermediate file once cli-install/% depends on it.
.PRECIOUS: cli/%

all: dataplane cli

proto-lint:
	@if command -v buf >/dev/null 2>&1; then \
		buf lint; \
	else \
		echo "WARN: 'buf' not found, skipping buf lint (install: https://buf.build/docs/installation)"; \
	fi
	go test ./lint/protobuf/cmd/protolint/
	go run ./lint/protobuf/cmd/protolint/ --exclude subprojects

proto-breaking:
	@if command -v buf >/dev/null 2>&1; then \
		buf breaking --against ".git#branch=main"; \
	else \
		echo "WARN: 'buf' not found, skipping buf breaking (install: https://buf.build/docs/installation)"; \
	fi

proto-go:
	@command -v protoc >/dev/null 2>&1 || { echo "ERROR: protoc not found (install protobuf-compiler)"; exit 1; }
	@command -v protoc-gen-go >/dev/null 2>&1 || { echo "ERROR: protoc-gen-go not found (go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11)"; exit 1; }
	@command -v protoc-gen-go-grpc >/dev/null 2>&1 || { echo "ERROR: protoc-gen-go-grpc not found (go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1)"; exit 1; }
	@set -e; protos=$$(find . \
		\( -path './subprojects' -o -path './build*' -o -type d -name '.?*' \) -prune \
		-o -name '*.proto' -print \
		| sort); \
		test -n "$$protos"; \
		protoc -I . \
			--go_out=paths=source_relative:. \
			--go-grpc_out=paths=source_relative:. \
			$$protos

lint-go:
	go test ./lint/style/cmd/stylelint/
	go run ./lint/style/cmd/stylelint/
	@if command -v golangci-lint >/dev/null 2>&1; then \
		$(MAKE) proto-go; \
		golangci-lint run ./...; \
	else \
		echo "WARN: 'golangci-lint' not found, skipping modernize lint (install: https://golangci-lint.run/welcome/install/)"; \
	fi

# Reparses the configured build's compile database with clang -fsyntax-only.
#
# Every C job in CI builds with gcc, so a construct gcc accepts and clang
# rejects otherwise only surfaces overnight in the fuzzing workflow. This
# is front-end only, no codegen, so replaying it here costs seconds and
# needs no extra toolchain beyond the clang already on the job's image.
lint/clang-syntax:
	@command -v clang >/dev/null 2>&1 || { echo "ERROR: clang not found (install: apt install clang)"; exit 1; }
	@command -v jq >/dev/null 2>&1 || { echo "ERROR: jq not found (install: apt install jq)"; exit 1; }
	@test -f "$(COMPILE_DB)" || { echo "ERROR: $(COMPILE_DB) not found (run 'meson setup build' first)"; exit 1; }
	@set -e; \
	tmpdir=$$(mktemp -d); \
	trap 'rm -rf "$$tmpdir"' EXIT; \
	dir=$$(jq -r '.[0].directory' "$(COMPILE_DB)"); \
	jq -r '.[] | select(.file | endswith(".c")) | select(.file | contains("subprojects/") | not) | .command' "$(COMPILE_DB)" > "$$tmpdir/commands.txt"; \
	awk '{ \
			out = "clang"; started = 0; skip = 0; \
			for (i = 1; i <= NF; i++) { \
				tok = $$i; \
				if (!started) { if (tok ~ /^-/) started = 1; else continue } \
				if (skip) { skip = 0; continue } \
				if (tok == "-c" || tok == "-MD") continue; \
				if (tok == "-o" || tok == "-MF" || tok == "-MQ") { skip = 1; continue } \
				out = out " " tok; \
			} \
			print out " -fsyntax-only"; \
		}' "$$tmpdir/commands.txt" > "$$tmpdir/awk_out.txt"; \
	sort -u "$$tmpdir/awk_out.txt" > "$$tmpdir/cmds.txt"; \
	test -s "$$tmpdir/cmds.txt" || { echo "ERROR: no translation units extracted from $(COMPILE_DB)"; exit 1; }; \
	n=$$(wc -l < "$$tmpdir/cmds.txt"); \
	echo "clang-syntax: parsing $$n translation units with $$(clang --version | head -1)"; \
	(cd "$$dir" && xargs -a "$$tmpdir/cmds.txt" -d '\n' -P "$$(nproc)" -I{} sh -c '{}')

lint-commit:
	lint/commit/commitlint_test.sh
	lint/commit/commitlint.sh --range origin/main..HEAD

# Generate .claude/agents/*.md, .codex/agents/*.toml, and .opencode/agents/*.md from the tracked
# source of truth, .rulesync/subagents/*.md.
#
# Refuses when the source roster is empty: rulesync's delete:true wipes
# every already-generated charter and still exits success, and neither
# tree is tracked in git to restore from. Also refuses when a source
# charter is unloadable: a mutating generate silently deletes its output
# and still exits 0, so a '--check' preflight (a real dry run) is scanned
# for the diagnostic first.
ai/agents:
	@set -e; sources=$$(find .rulesync/subagents -maxdepth 1 -name '*.md'); \
		test -n "$$sources" || { echo "ERROR: .rulesync/subagents/*.md is empty; refusing to run 'rulesync generate', which would delete every generated charter" >&2; exit 1; }
	@out=$$($(RULESYNC) $(RULESYNC_GENERATE_ARGS) --check 2>&1); \
		echo "$$out"; \
		if echo "$$out" | grep -q 'Failed to load subagent file'; then \
			echo "ERROR: rulesync failed to load a source charter; refusing to run the mutating generate (see diagnostic above)" >&2; \
			exit 1; \
		fi
	$(RULESYNC) $(RULESYNC_GENERATE_ARGS)

# Reports whether .claude/agents/, .codex/agents/, and .opencode/agents/ have drifted from
# .rulesync/subagents/*.md, without writing to the filesystem.
#
# Run by .githooks/post-merge after a pull to warn about stale charters.
# Run 'make ai/agents' to refresh the generated trees.
lint/agents:
	$(RULESYNC) $(RULESYNC_GENERATE_ARGS) --check

hooks:
	git config core.hooksPath .githooks

go-cache-clean:
	go clean -cache

setup:
	meson setup $(EXTRA_MODULES_FLAG) build

setup-debug:
	@if [ ! -d "build" ]; then \
		meson setup -Dbuildtype=debug -Doptimization=0 $(EXTRA_MODULES_FLAG) build; \
	else \
		meson configure -Dbuildtype=debug -Doptimization=0 -Db_sanitize="" $(EXTRA_MODULES_FLAG) build; \
	fi

setup-asan:
	meson setup -Dbuildtype=debug -Doptimization=0 -Db_sanitize=address,undefined $(EXTRA_MODULES_FLAG) build

dataplane:
	meson compile -C build

cli: cli-build

cli-build:
	$(CARGO) build --release --workspace
	@set -eu; \
	for m in $(EXTRA_CLI_MODULES); do \
		CARGO_TARGET_DIR=$(abspath $(TARGET_DIR)) $(CARGO) build --release --manifest-path modules/$$m/cli/Cargo.toml; \
	done

# Optional convenience target:
#   make cli/acl
#   make cli/forward
#
# It builds package yanet-cli-<module>, or, for a private module CLI that is
# not a workspace member, builds its standalone manifest directly with the
# repo's target/ directory so the binary still lands in $(RELEASE_DIR).
cli/%:
	CARGO_TARGET_DIR=$(abspath $(TARGET_DIR)) $(CARGO) build --release $(if $(filter $*,$(EXTRA_CLI_MODULES)),--manifest-path modules/$*/cli/Cargo.toml,--package yanet-cli-$*)

# Installs all CLI binaries.
cli-install:
	@install -d "$(DESTDIR)$(BINDIR)"
	@set -eu; \
	for bin in $(CLI_BINARIES); do \
		src="$(RELEASE_DIR)/$$bin"; \
		dst="$(DESTDIR)$(BINDIR)/$$bin"; \
		printf 'INSTALL %-10s %s -> %s\n' '' "$$bin" "$$dst"; \
		install -m 755 "$$src" "$$dst"; \
	done

# Backward-compatible name.
cli-core-install: cli-install

# Installs one module CLI binary:
#   make cli-install/acl
#
# It will build only package yanet-cli-acl and install only that binary.
cli-install/%: cli/%
	@install -d "$(DESTDIR)$(BINDIR)"
	@src="$(RELEASE_DIR)/yanet-cli-$*"; \
	dst="$(DESTDIR)$(BINDIR)/yanet-cli-$*"; \
	printf 'INSTALL %-10s %s -> %s\n' '' "yanet-cli-$*" "$$dst"; \
	install -m 755 "$$src" "$$dst"

cli-clean:
	$(CARGO) clean

# Cargo does not have per-package clean on stable in a useful universal form,
# so keep this as a compatibility target.
cli-clean/%:
	$(CARGO) clean || true

# Wipes the Go build cache first: a stale entry can link a stale C archive.
#
# The clean is a recipe step rather than a prerequisite so that -j cannot run
# it concurrently with the build. A caller that has already cleaned once, such
# as a CI job, invokes the -only target instead.
test:
	$(MAKE) go-cache-clean
	$(MAKE) test-only

test-only: dataplane
	CGO_CPPFLAGS="$(strip $(CGO_CPPFLAGS) $(YANET_CACHE_LINE_CPPFLAG))" go test -count=1 $$(go list ./... | grep -v '^github.com/yanet-platform/yanet2/tests/functional/main')
	meson test -C build

test-asan:
	$(MAKE) go-cache-clean
	$(MAKE) test-asan-only

test-asan-only:
	@if [ ! -d "build" ]; then \
		$(MAKE) setup-asan; \
	else \
		meson configure -Dbuildtype=debug -Doptimization=0 -Dfuzzing=disabled -Db_sanitize=address,undefined $(EXTRA_MODULES_FLAG) build; \
	fi
	meson compile -C build
# Set as a default only, mirroring the same if-absent condition meson uses for
# its own injection: without it, a recoverable diagnostic would go unseen and
# the package would still pass.
	CGO_CPPFLAGS="$(strip $(CGO_CPPFLAGS) $(YANET_CACHE_LINE_CPPFLAG))" CGO_CFLAGS="-fsanitize=address,undefined" CGO_LDFLAGS="-fsanitize=address,undefined" UBSAN_OPTIONS="$${UBSAN_OPTIONS:-halt_on_error=1:abort_on_error=1:print_summary=1:print_stacktrace=1}" go test -count=1 $$(go list ./... | grep -v '^github.com/yanet-platform/yanet2/tests/functional/main')
	meson test -C build

test-tsan:
	@if [ ! -d "build-tsan" ]; then \
		meson setup build-tsan -Dbuildtype=debug -Doptimization=0 -Db_sanitize=thread $(EXTRA_MODULES_FLAG); \
	else \
		meson configure -Dbuildtype=debug -Doptimization=0 -Db_sanitize=thread $(EXTRA_MODULES_FLAG) build-tsan; \
	fi
	meson test -C build-tsan --suite common --suit fwstate --no-suite large

test-functional:
	@echo "Running functional tests..."
	cd tests/functional && $(MAKE) test

# Run Go benchmarks without running unit tests.
# A/B recipe: save baseline and candidate runs to files and compare with benchstat.
#   go test -run='^$$' -bench=. -benchmem -count=6 ./bindings/go/dataplane_ut/... > old.txt
#   go test -run='^$$' -bench=. -benchmem -count=6 ./bindings/go/dataplane_ut/... > new.txt
#   benchstat old.txt new.txt
bench:
	$(MAKE) go-cache-clean
	$(MAKE) dataplane
	CGO_CPPFLAGS="$(strip $(CGO_CPPFLAGS) $(YANET_CACHE_LINE_CPPFLAG))" go test -run='^$$' -bench=. -benchmem ./bindings/go/dataplane_ut/... ./modules/acl/tests/functional/...

fuzz:
	@if [ -d build ] && ! meson introspect build --buildoptions | jq -er '.[] | select(.name=="fuzzing") | .value' | grep -q enabled; then \
		echo "Wiping build for fuzzing..."; \
		rm -rf build; \
	fi
	@if [ ! -d build ]; then \
		env CC=clang CXX=clang++ meson setup -Dbuildtype=debug -Doptimization=0 -Dfuzzing=enabled $(EXTRA_MODULES_FLAG) build; \
	fi
	env CC=clang CXX=clang++ meson compile -C build
	@echo "Ready to fuzz the following modules:"
	@find build/tests/fuzzing/ -type f -executable -printf '%f\n'
	@if [ -n "$(MODULE)" ]; then \
		mkdir -p corpus; \
		./build/tests/fuzzing/$(MODULE) corpus/; \
	fi

install1:
	cp build/dataplane/yanet-dataplane /usr/bin
	cp build/controlplane/yanet-controlplane /usr/bin
	$(MAKE) cli-install PREFIX=/usr

install: dataplane cli-install
	meson install -C build --skip-subprojects

clean: go-cache-clean cli-clean
	@echo "Cleaning build directories..."
	rm -rf build/
	rm -rf buildfuzz/
