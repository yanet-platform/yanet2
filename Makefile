.PHONY: all dataplane controlplane test cli

all: go-cache-clean dataplane controlplane cli

go-cache-clean:
	go clean -cache

dataplane:
	meson compile -C build

controlplane: dataplane	
	$(MAKE) -C modules/decap controlplane
	$(MAKE) -C modules/route controlplane
	$(MAKE) -C modules/forward controlplane
	$(MAKE) -C modules/nat64 controlplane
	$(MAKE) -C controlplane

cli:
	$(MAKE) -C cli
	$(MAKE) -C modules/route cli
	$(MAKE) -C modules/decap cli
	$(MAKE) -C modules/forward cli
	$(MAKE) -C modules/nat64 cli

cli-install:
	$(MAKE) -C cli install
	$(MAKE) -C modules/route/cli install
	$(MAKE) -C modules/decap/cli install
	$(MAKE) -C modules/forward/cli install
	$(MAKE) -C modules/nat64/cli install

test: dataplane
	meson test -C build
	echo "Controlplane tests"
	go -C controlplane test ./...
