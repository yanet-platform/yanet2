.PHONY: all dataplane controlplane test cli

all: go-cache-clean dataplane controlplane cli

go-cache-clean:
	go clean -cache

dataplane:
	meson compile -C build

controlplane: dataplane
	$(MAKE) -C controlplane
	$(MAKE) -C modules/decap controlplane
	$(MAKE) -C modules/route controlplane

cli:
	$(MAKE) -C cli
	$(MAKE) -C modules/route cli
	$(MAKE) -C modules/decap cli

cli-install:
	$(MAKE) -C cli install
	$(MAKE) -C modules/route/cli install
	$(MAKE) -C modules/decap/cli install

test: dataplane
	meson test -C build
	echo "Controlplane tests"
	go -C controlplane test ./...
