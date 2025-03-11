.PHONY: all dataplane controlplane test

all: dataplane controlplane

dataplane:
	meson compile -C build

controlplane: dataplane
	$(MAKE) -C controlplane

test: dataplane
	meson test -C build
