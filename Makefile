CARGO ?= cargo

# Default PREFIX for debian packaging
PREFIX ?= /usr
BINDIR ?= $(PREFIX)/bin

TARGET_DIR ?= target
RELEASE_DIR := $(TARGET_DIR)/release

# Core CLI packages/binaries that live in cli workspace.
CLI_CORE_MODULES := \
	common \
	inspect \
	pipeline \
	function \
	counters

CLI_CORE_BINARIES := yanet-cli $(addprefix yanet-cli-,$(CLI_CORE_MODULES))

# Module CLI packages/binaries.
#
# These are modules that historically had modules/<module>/cli/Makefile.
# If a new module CLI appears, add its module name here.
CLI_MODULES := \
	acl \
	balancer \
	decap \
	device-plain \
	device-vlan \
	dscp \
	fwstate \
	neighbour \
	route \
	route-mpls \
	forward \
	nat64 \
	pdump

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
	test \
	test-asan \
	test-tsan \
	test-functional \
	fuzz \
	cli \
	cli-build \
	cli-install \
	cli-core-install \
	cli-clean \
	$(addprefix cli/,$(CLI_MODULES)) \
	$(addprefix cli-install/,$(CLI_MODULES)) \
	$(addprefix cli-clean/,$(CLI_MODULES))

all: dataplane cli

proto-lint:
	@find . -name '*.proto' -print0 | xargs -0 clang-format --dry-run --Werror
	go test ./lint/protobuf/cmd/protolint/
	go run ./lint/protobuf/cmd/protolint/ --exclude subprojects

go-cache-clean:
	go clean -cache

setup:
	meson setup build

setup-debug:
	@if [ ! -d "build" ]; then \
		meson setup -Dbuildtype=debug -Doptimization=0 build; \
	else \
		meson configure -Dbuildtype=debug -Doptimization=0 -Db_sanitize="" build; \
	fi

setup-asan:
	meson setup -Dbuildtype=debug -Doptimization=0 -Db_sanitize=address,undefined build

dataplane:
	meson compile -C build

cli: cli-build

cli-build:
	$(CARGO) build --release --workspace

# Optional convenience target:
#   make cli/acl
#   make cli/forward
#
# It builds package yanet-cli-<module>.
cli/%:
	$(CARGO) build --release --package yanet-cli-$*

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
	go test -count=1 $$(go list ./... | grep -v 'tests/functional')
	meson test -C build

test-asan: go-cache-clean
	@if [ ! -d "build" ]; then \
		$(MAKE) setup-asan; \
	else \
		meson configure -Dbuildtype=debug -Doptimization=0 -Dfuzzing=disabled -Db_sanitize=address,undefined build; \
	fi
	meson compile -C build
	CGO_CFLAGS="-fsanitize=address,undefined" CGO_LDFLAGS="-fsanitize=address,undefined" go test -count=1 $$(go list ./... | grep -v 'tests/functional')
	meson test -C build

test-tsan:
	@if [ ! -d "build-tsan" ]; then \
		meson setup build-tsan -Dbuildtype=debug -Doptimization=0 -Db_sanitize=thread; \
	else \
		meson configure -Dbuildtype=debug -Doptimization=0 -Db_sanitize=thread build-tsan; \
	fi
	meson test -C build-tsan --suite common --suit fwstate --no-suite large

test-functional:
	@echo "Running functional tests..."
	cd tests/functional && $(MAKE) test

fuzz:
	@if [ -d build ] && ! meson introspect build --buildoptions | jq -er '.[] | select(.name=="fuzzing") | .value' | grep -q enabled; then \
		echo "Wiping build for fuzzing..."; \
		rm -rf build; \
	fi
	@if [ ! -d build ]; then \
		env CC=clang CXX=clang++ meson setup -Dbuildtype=debug -Doptimization=0 -Dfuzzing=enabled build; \
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
	install -m 644 controlplane/etc/yanet/controlplane-director.yaml $(DESTDIR)/etc/yanet2/controlplane.yaml
	install -m 644 dataplane.yaml $(DESTDIR)/etc/yanet2/dataplane.yaml
	install -m 644 controlplane/etc/yanet/bird-adapter.yaml $(DESTDIR)/etc/yanet2/bird-adapter.yaml
	install -m 644 agents/yanet-pipeline-operator/etc/yanet/yanet-pipeline-operator.yaml $(DESTDIR)/etc/yanet2/yanet-pipeline-operator.yaml
	install -d $(DESTDIR)/etc/yanet2/forward.d
	install -m 644 agents/yanet-forward-operator/etc/yanet/forward.d/vlan-phy.yaml $(DESTDIR)/etc/yanet2/forward.d/vlan-phy.yaml
	install -m 644 agents/yanet-forward-operator/etc/yanet/forward.d/phy-vlan.yaml $(DESTDIR)/etc/yanet2/forward.d/phy-vlan.yaml

clean: go-cache-clean cli-clean
	@echo "Cleaning build directories..."
	rm -rf build/
	rm -rf buildfuzz/
