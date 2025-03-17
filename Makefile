.PHONY: all dataplane controlplane test

all: go-cache-clean dataplane controlplane

go-cache-clean:
	go clean -cache

dataplane:
	meson compile -C build

controlplane: dataplane
	$(MAKE) -C controlplane

test: dataplane
	meson test -C build
