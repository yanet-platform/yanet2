all: $(objects)
	meson compile -C build

test: all
	cd tests/go && \
	go test -count=1 ./...
