CARGO ?= cargo

# Compile database consumed by lint/clang-syntax.
COMPILE_DB ?= build/compile_commands.json
# The cgo preprocessor flags meson builds Go with, which go test needs as well.
YANET_CGO_CPPFLAGS = $$(cat build/yanet_cgo_cppflags)

# Default PREFIX for debian packaging
PREFIX ?= /usr
BINDIR ?= $(PREFIX)/bin

TARGET_DIR ?= target
RELEASE_DIR := $(TARGET_DIR)/release

# Preserve literal inherited defaults; leave absent defaults to Go's environment.
ifneq ($(origin GOFLAGS), undefined)
override GOFLAGS := $(value GOFLAGS)
export GOFLAGS
endif

# Test arguments are whitespace-separated data, never shell or make expressions.
override GO_TEST_FLAGS := $(value GO_TEST_FLAGS)
override GO_TEST_PACKAGES := $(value GO_TEST_PACKAGES)
export GO_TEST_FLAGS GO_TEST_PACKAGES

# Core CLI packages/binaries that live in cli workspace.
CLI_CORE_MODULES := \
	common \
	device-list \
	device-delete \
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
	blackhole \
	decap \
	device-plain \
	device-vlan \
	device-vxlan \
	device-trafgen \
	dscp \
	fwstate \
	fwstatemap \
	mirror \
	l3b \
	route \
	route-mpls \
	forward \
	nat64 \
	pdump \
	ring \
	unrdup \
	operator-neighbour \
	operator-pipeline \
	operator-route

# Public module directories: the ones modules/meson.build names.
#
# Anything else under modules/ is a private, gitignored module (see
# modules/meson.build's extra_modules option).
PUBLIC_MODULES := $(shell sed -n "s/^[[:space:]\#]*subdir('\([^']*\)').*/\1/p" modules/meson.build)

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
	build \
	dataplane \
	go-test-prepare \
	install \
	clean \
	go-cache-clean \
	proto-lint \
	proto-breaking \
	proto-go \
	lint-go \
	lint/clang-syntax \
	lint-commit \
	gate \
	hooks \
	test \
	test-only \
	test-recipes \
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

all: build cli

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
		protoc --experimental_allow_proto3_optional -I . \
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

# Runs the CI checks for the layers changed since origin/main and prints
# only what fails.
gate:
	@scripts/gate.sh

hooks:
	git config core.hooksPath .githooks

go-cache-clean:
	go clean -cache

setup:
	@if [ -f build/build.ninja ]; then \
		meson configure $(MESON_SETUP_FLAGS) $(EXTRA_MODULES_FLAG) build && meson setup --reconfigure build; \
	else \
		meson setup $(MESON_SETUP_FLAGS) $(EXTRA_MODULES_FLAG) build; \
	fi

setup-debug:
	@if [ ! -d "build" ]; then \
		meson setup -Dbuildtype=debug -Doptimization=0 $(EXTRA_MODULES_FLAG) build; \
	else \
		meson configure -Dbuildtype=debug -Doptimization=0 -Db_sanitize=none -Dfuzzing=disabled $(EXTRA_MODULES_FLAG) build; \
	fi

setup-asan:
	meson setup -Dbuildtype=debug -Doptimization=0 -Db_sanitize=address,undefined $(EXTRA_MODULES_FLAG) build

# Everything meson builds: the dataplane, the Go binaries, the cgo link flags and
# the C tests.
build:
	meson compile -C build

# The older name of build.
dataplane: build

go-test-prepare:
	meson compile -C build go-test-prepare
	@test -f build/yanet_cgo_cppflags || { echo "ERROR: build/yanet_cgo_cppflags not found: go tests need this checkout's own build/, not a link to another one (remove it and run 'meson setup build')"; exit 1; }

cli: cli-build

# Cargo profile of the workspace CLI build; CI passes `ci`.
#
# The ci profile puts the workspace binaries in $(TARGET_DIR)/ci, where
# cli-install does not look; private module CLIs always build with release.
CLI_PROFILE ?= release

# Binaries only: the build scripts compile their helper for the host, so it
# needs no second, target-side build.
cli-build:
	$(CARGO) build --profile $(CLI_PROFILE) --workspace --bins
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

test:
	$(MAKE) test-only

# Discovery and tests share build context without reinterpreting option data.
#
# Go 1.27.1 option arity separates build flags from test-only values and literal
# binary tails; unknown automatic-selection options fail explicitly.
test-only: go-test-prepare
	@set -eu; set -f; \
	export CGO_CPPFLAGS="$(strip $(CGO_CPPFLAGS) $(YANET_CGO_CPPFLAGS))"; \
	packages="$$GO_TEST_PACKAGES"; \
	set -- $$GO_TEST_FLAGS; \
	while [ "$$#" -gt 0 ]; do \
		option=$$1; shift; \
		case "$$option" in \
			-args|--args|--) break ;; \
			-C|-C=*|--C|--C=*) printf '%s\n' 'ERROR: -C is not supported in GO_TEST_FLAGS' >&2; exit 1 ;; \
			*=*) ;; \
			-asmflags|--asmflags|-buildmode|--buildmode|-compiler|--compiler|-covermode|--covermode|-coverpkg|--coverpkg| \
			-gccgoflags|--gccgoflags|-gcflags|--gcflags|-installsuffix|--installsuffix|-ldflags|--ldflags|-mod|--mod| \
			-modfile|--modfile|-overlay|--overlay|-p|--p|-pgo|--pgo|-pkgdir|--pkgdir|-tags|--tags|-toolexec|--toolexec| \
			-bench|--bench|-benchtime|--benchtime|-blockprofile|--blockprofile|-blockprofilerate|--blockprofilerate| \
			-count|--count|-coverprofile|--coverprofile|-cpu|--cpu|-cpuprofile|--cpuprofile|-exec|--exec| \
			-fuzz|--fuzz|-fuzzminimizetime|--fuzzminimizetime|-fuzztime|--fuzztime|-list|--list| \
			-memprofile|--memprofile|-memprofilerate|--memprofilerate|-mutexprofile|--mutexprofile| \
			-mutexprofilefraction|--mutexprofilefraction|-o|--o|-outputdir|--outputdir|-parallel|--parallel| \
			-run|--run|-shuffle|--shuffle|-skip|--skip|-timeout|--timeout|-trace|--trace|-vet|--vet| \
			-test.bench|--test.bench|-test.benchtime|--test.benchtime|-test.blockprofile|--test.blockprofile| \
			-test.blockprofilerate|--test.blockprofilerate|-test.count|--test.count|-test.coverprofile|--test.coverprofile| \
			-test.cpu|--test.cpu|-test.cpuprofile|--test.cpuprofile|-test.fuzz|--test.fuzz| \
			-test.fuzzminimizetime|--test.fuzzminimizetime|-test.fuzztime|--test.fuzztime|-test.list|--test.list| \
			-test.memprofile|--test.memprofile|-test.memprofilerate|--test.memprofilerate|-test.mutexprofile|--test.mutexprofile| \
			-test.mutexprofilefraction|--test.mutexprofilefraction|-test.outputdir|--test.outputdir| \
			-test.parallel|--test.parallel|-test.run|--test.run|-test.shuffle|--test.shuffle|-test.skip|--test.skip| \
			-test.timeout|--test.timeout|-test.trace|--test.trace) \
				if [ "$$#" -gt 0 ]; then shift; fi ;; \
		esac; \
	done; \
	if [ -z "$$packages" ]; then \
		list_flags=; set -- $$GO_TEST_FLAGS; \
		while [ "$$#" -gt 0 ]; do \
			option=$$1; shift; \
			case "$$option" in -args|--args|--) break ;; esac; \
			case "$$option" in \
				--*) name=$${option#--} ;; \
				-*) name=$${option#-} ;; \
				*) printf 'ERROR: unsupported automatic Go selection argument: %s\n' "$$option" >&2; exit 1 ;; \
			esac; \
			name=$${name%%=*}; project=false; \
			case "$$name" in \
				a|asan|buildvcs|cover|linkshared|modcacherw|msan|race|trimpath|work|x) \
					project=true; arity=bool ;; \
				asmflags|buildmode|compiler|covermode|coverpkg|gccgoflags|gcflags|installsuffix|ldflags|mod|modfile|overlay|p|pgo|pkgdir|tags|toolexec) \
					project=true; arity=value ;; \
				artifacts|benchmem|c|failfast|fullpath|json|short|v|test.artifacts|test.benchmem|test.failfast|test.fullpath|test.short|test.v) \
					arity=bool ;; \
				bench|benchtime|blockprofile|blockprofilerate|count|coverprofile|cpu|cpuprofile|exec|fuzz|fuzzminimizetime|fuzztime|list| \
				memprofile|memprofilerate|mutexprofile|mutexprofilefraction|o|outputdir|parallel|run|shuffle|skip|timeout|trace|vet| \
				test.bench|test.benchtime|test.blockprofile|test.blockprofilerate|test.count|test.coverprofile|test.cpu|test.cpuprofile| \
				test.fuzz|test.fuzzminimizetime|test.fuzztime|test.list|test.memprofile|test.memprofilerate|test.mutexprofile| \
				test.mutexprofilefraction|test.outputdir|test.parallel|test.run|test.shuffle|test.skip|test.timeout|test.trace) \
					arity=value ;; \
				*) printf 'ERROR: unsupported automatic Go selection option: %s\n' "$$option" >&2; exit 1 ;; \
			esac; \
			case "$$option" in \
				*=*) value=$${option#*=} ;; \
				*) value=true; \
					if [ "$$arity" = value ]; then \
						if [ "$$#" -eq 0 ]; then \
							printf 'ERROR: Go selection option needs a value: %s\n' "$$option" >&2; exit 1; \
						fi; \
						value=$$1; shift; \
					fi ;; \
			esac; \
			if [ "$$project" = true ]; then list_flags="$$list_flags -$$name=$$value"; fi; \
		done; \
		packages=$$(go list -f '{{.ImportPath}}' -json=false $$list_flags ./...); \
		packages=$$(printf '%s\n' "$$packages" | grep -v '^github.com/yanet-platform/yanet2/tests/functional/main'); \
	fi; \
	set -- $$packages; \
	if [ "$$#" -eq 0 ]; then \
		printf '%s\n' 'ERROR: no Go test packages selected' >&2; exit 1; \
	fi; \
	for package in "$$@"; do \
		case "$$package" in \
			-*) printf 'ERROR: option-shaped Go package entry: %s\n' "$$package" >&2; exit 1 ;; \
		esac; \
	done; \
	go test -count=1 "$$@" $$GO_TEST_FLAGS
	meson test -C build

test-recipes:
	python3 tests/recipes/dtest_test.py

test-asan:
	$(MAKE) test-asan-only

# Set a default sanitizer policy while preserving inherited Go flags and tags.
#
# -asan instruments the Go side too, so a Go access through unsafe past a C
# allocation, or into a block the C allocator freed, aborts the test. It also
# turns on the leak check at exit, so C memory a test never frees fails it.
test-asan-only:
	@if [ ! -d "build" ]; then \
		$(MAKE) setup-asan; \
	else \
		meson configure -Dbuildtype=debug -Doptimization=0 -Dfuzzing=disabled -Db_sanitize=address,undefined $(EXTRA_MODULES_FLAG) build; \
	fi
	$(MAKE) go-test-prepare
	@set -eu; \
	export CGO_CPPFLAGS="$(strip $(CGO_CPPFLAGS) $(YANET_CGO_CPPFLAGS))"; \
	export CGO_CFLAGS="-fsanitize=address,undefined"; \
	export CGO_LDFLAGS="-fsanitize=address,undefined"; \
	export UBSAN_OPTIONS="$${UBSAN_OPTIONS:-halt_on_error=1:abort_on_error=1:print_summary=1:print_stacktrace=1}"; \
	go test -asan -count=1 $$(go list ./... | grep -v '^github.com/yanet-platform/yanet2/tests/functional/main')
	meson test -C build

test-tsan:
	@if [ ! -d "build-tsan" ]; then \
		meson setup build-tsan -Dbuildtype=debug -Doptimization=0 -Db_sanitize=thread $(EXTRA_MODULES_FLAG); \
	else \
		meson configure -Dbuildtype=debug -Doptimization=0 -Db_sanitize=thread $(EXTRA_MODULES_FLAG) build-tsan; \
	fi
	meson test -C build-tsan --suite common --suite fwstate

test-functional:
	@echo "Running functional tests..."
	cd tests/functional && $(MAKE) test

# Run Go benchmarks without running unit tests.
# A/B recipe: save baseline and candidate runs to files and compare with benchstat.
#   go test -run='^$$' -bench=. -benchmem -count=6 ./bindings/go/dataplane_ut/... > old.txt
#   go test -run='^$$' -bench=. -benchmem -count=6 ./bindings/go/dataplane_ut/... > new.txt
#   benchstat old.txt new.txt
bench:
	$(MAKE) go-test-prepare
	CGO_CPPFLAGS="$(strip $(CGO_CPPFLAGS) $(YANET_CGO_CPPFLAGS))" go test -run='^$$' -bench=. -benchmem ./bindings/go/dataplane_ut/... ./modules/acl/tests/functional/...

# Reconfigure an existing fuzz build so its Go targets keep current flags.
fuzz:
	@if [ -d build ] && ! meson introspect build --buildoptions | jq -er '.[] | select(.name=="fuzzing") | .value' | grep -q enabled; then \
		echo "Wiping build for fuzzing..."; \
		rm -rf build; \
	fi
	@if [ ! -d build ]; then \
		env CC=clang CXX=clang++ meson setup -Dbuildtype=debug -Doptimization=0 -Dfuzzing=enabled $(EXTRA_MODULES_FLAG) build; \
	else \
		env CC=clang CXX=clang++ meson setup -Dbuildtype=debug -Doptimization=0 -Dfuzzing=enabled $(EXTRA_MODULES_FLAG) --reconfigure build; \
	fi
	env CC=clang CXX=clang++ meson compile -C build
	@echo "Ready to fuzz the following modules:"
	@find build/tests/fuzzing/ -type f -executable -printf '%f\n'
	@if [ -n "$(MODULE)" ]; then \
		mkdir -p corpus; \
		./build/tests/fuzzing/$(MODULE) corpus/; \
	fi

install: build cli-install
	meson install -C build --skip-subprojects

clean: go-cache-clean cli-clean
	@echo "Cleaning build directories..."
	rm -rf build/
	rm -rf build-tsan/
