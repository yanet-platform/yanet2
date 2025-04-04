.PHONY: all dataplane controlplane test cli

all: go-cache-clean dataplane controlplane cli

go-cache-clean:
	go clean -cache

dataplane:
	meson compile -C build

controlplane: dataplane
	$(MAKE) -C controlplane

cli:
	$(MAKE) -C cli

test: dataplane
	meson test -C build
	echo "Controlplane tests"
	go -C controlplane test ./...
