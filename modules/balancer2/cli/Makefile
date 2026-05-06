CARGO  := cargo
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

ROOT_DIR    := ../../..
TARGET_DIR  := $(ROOT_DIR)/target
RELEASE_DIR := $(TARGET_DIR)/release

build:
	$(CARGO) build --release --package yanet-cli-balancer2

install:
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(RELEASE_DIR)/yanet-cli-balancer2 $(DESTDIR)$(BINDIR)/

clean:
	$(CARGO) clean || true

.PHONY: build install clean
