.PHONY: all dataplane test test-functional cli cli-install fuzz clean $(foreach module,$(MODULES),cli/$(module) cli-install/$(module))

# Define the list of modules to avoid repetition
MODULES := decap dscp route forward nat64 pdump acl fwstate

# Default PREFIX for debian packaging
PREFIX ?= /usr

all: dataplane cli

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

install1:
	cp build/dataplane/yanet-dataplane /usr/bin
	cp build/controlplane/yanet-controlplane /usr/bin
	cd cli && make install

cli:
	cargo build --release --workspace

cli-install: cli-core-install $(foreach module,$(MODULES),cli-install/$(module))

cli-core-install:
	$(MAKE) -C cli install PREFIX=$(PREFIX)

cli-install/%:
	$(MAKE) -C modules/$*/cli install PREFIX=$(PREFIX)

cli-clean/%:
	$(MAKE) -C modules/$*/cli clean

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

test-functional:
	@echo "Running functional tests..."
	cd tests/functional && $(MAKE) test

fuzz:
	@if [ -d build ] && ! meson introspect build --buildoptions | jq -er '.[] | select(.name=="fuzzing") | .value'|grep -q enabled; then \
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

install: dataplane cli-install
	meson install -C build --skip-subprojects
	install -d $(DESTDIR)/etc/yanet2
	install -m 644 controlplane.yaml $(DESTDIR)/etc/yanet2/controlplane.yaml
	install -m 644 dataplane.yaml $(DESTDIR)/etc/yanet2/dataplane.yaml

clean: go-cache-clean $(foreach module,$(MODULES),cli-clean/$(module))
	@echo "Cleaning build directories..."
	rm -rf build/
	rm -rf buildfuzz/
	$(MAKE) -C cli clean
