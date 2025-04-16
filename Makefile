.PHONY: all dataplane test cli cli-install $(foreach module,$(MODULES),cli/$(module) cli-install/$(module))

# Define the list of modules to avoid repetition
MODULES := decap route forward nat64

all: go-cache-clean dataplane cli

go-cache-clean:
	go clean -cache

dataplane:
	meson compile -C build

cli:
	cargo build --release --workspace --exclude=yanetweb

cli-install: $(foreach module,$(MODULES),cli-install/$(module))

cli-install/%:
	$(MAKE) -C modules/$*/cli install

test: dataplane
	meson test -C build
