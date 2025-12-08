CARGO  := cargo
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

ROOT_DIR    := ../../..
TARGET_DIR  := $(ROOT_DIR)/target
RELEASE_DIR := $(TARGET_DIR)/release
BUILD_DIR   := $(ROOT_DIR)/build/modules/balancer/cli

MODULES  := balancer
BINARIES := $(addprefix yanet-cli-,$(MODULES))
TARGETS  := $(addprefix build/,$(BINARIES))

build: $(TARGETS)

build/%:
	$(CARGO) build --release --package $*

install:
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(addprefix $(RELEASE_DIR)/,$(BINARIES)) $(DESTDIR)$(BINDIR)/

clean:
	$(CARGO) clean || true

.PHONY: build install clean
