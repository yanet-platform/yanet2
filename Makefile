all: $(objects)
	meson compile -C build

test: all
	meson test -C build
