.PHONY: all dataplane test cli cli-install fuzz $(foreach module,$(MODULES),cli/$(module) cli-install/$(module))

# Define the list of modules to avoid repetition
MODULES := decap dscp route forward nat64

all: go-cache-clean dataplane cli

go-cache-clean:
	go clean -cache

dataplane:
	meson compile -C build

cli:
	cargo build --release --workspace --exclude=yanetweb

cli-install: cli-core-install $(foreach module,$(MODULES),cli-install/$(module))

cli-core-install:
	$(MAKE) -C cli install

cli-install/%:
	$(MAKE) -C modules/$*/cli install

test: dataplane
	meson test -C build

fuzz:
	env CC=clang CXX=clang++ meson setup -Dfuzzing=enabled  buildfuzz
	env CC=clang CXX=clang++ meson compile -C buildfuzz
