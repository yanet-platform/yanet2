CARGO ?= cargo

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
	mirror \
	route \
	route-mpls \
	forward \
	nat64 \
	pdump \
	operator-decap \
	operator-forward \
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
	route-mpls

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
	hooks \
	test \
	test-asan \
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
	@command -v protoc-gen-go >/dev/null 2>&1 || { echo "ERROR: protoc-gen-go not found (go install google.golang.org/protobuf/cmd/protoc-gen-go@latest)"; exit 1; }
	@command -v protoc-gen-go-grpc >/dev/null 2>&1 || { echo "ERROR: protoc-gen-go-grpc not found (go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest)"; exit 1; }
	@set -e; protos=$$(find . \
		\( -path './subprojects' -o -path './build*' -o -path './.git' \) -prune \
		-o -name '*.proto' -print \
		| sort); \
		test -n "$$protos"; \
		protoc -I . \
			--go_out=paths=source_relative:. \
			--go-grpc_out=paths=source_relative:. \
			$$protos

lint-go:
	go test ./lint/logger/cmd/loglint/
	go run ./lint/logger/cmd/loglint/
	@if command -v golangci-lint >/dev/null 2>&1; then \
		$(MAKE) proto-go; \
		golangci-lint run ./...; \
	else \
		echo "WARN: 'golangci-lint' not found, skipping modernize lint (install: https://golangci-lint.run/welcome/install/)"; \
	fi

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

test: go-cache-clean dataplane
	go test -count=1 $$(go list ./... | grep -v '^github.com/yanet-platform/yanet2/tests/functional')
	meson test -C build

test-asan: go-cache-clean
	@if [ ! -d "build" ]; then \
		$(MAKE) setup-asan; \
	else \
		meson configure -Dbuildtype=debug -Doptimization=0 -Dfuzzing=disabled -Db_sanitize=address,undefined $(EXTRA_MODULES_FLAG) build; \
	fi
	meson compile -C build
	CGO_CFLAGS="-fsanitize=address,undefined" CGO_LDFLAGS="-fsanitize=address,undefined" go test -count=1 $$(go list ./... | grep -v '^github.com/yanet-platform/yanet2/tests/functional')
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
bench: go-cache-clean dataplane
	go test -run='^$$' -bench=. -benchmem ./bindings/go/dataplane_ut/... ./modules/acl/tests/functional/...

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
	install -d $(DESTDIR)/etc/yanet2
	install -m 644 controlplane/etc/yanet/controlplane-default.yaml $(DESTDIR)/etc/yanet2/controlplane-default.yaml
	install -m 644 dataplane.yaml $(DESTDIR)/etc/yanet2/dataplane-default.yaml
	install -m 644 operators/bird-adapter/etc/yanet/bird-adapter-default.yaml $(DESTDIR)/etc/yanet2/bird-adapter-default.yaml
	install -m 644 operators/pipeline/etc/yanet/yanet-pipeline-operator-default.yaml $(DESTDIR)/etc/yanet2/yanet-pipeline-operator-default.yaml
	install -m 644 operators/route/etc/yanet/yanet-route-operator-default.yaml $(DESTDIR)/etc/yanet2/yanet-route-operator-default.yaml
	install -d $(DESTDIR)/etc/yanet2/forward.d
	install -m 644 operators/forward/etc/yanet/forward.d/vlan-phy-default.yaml $(DESTDIR)/etc/yanet2/forward.d/vlan-phy-default.yaml
	install -m 644 operators/forward/etc/yanet/forward.d/phy-vlan-default.yaml $(DESTDIR)/etc/yanet2/forward.d/phy-vlan-default.yaml
	install -d $(DESTDIR)/etc/yanet2/decap.d
	install -m 644 operators/decap/etc/yanet/decap.d/default.yaml $(DESTDIR)/etc/yanet2/decap.d/default.yaml

clean: go-cache-clean cli-clean
	@echo "Cleaning build directories..."
	rm -rf build/
	rm -rf buildfuzz/
