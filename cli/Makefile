CARGO ?= $(shell which cargo)
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

MODULES := common route balancer inspect decap dscp forward pipeline function nat64 pdump counters acl fwstate device device-plain device-vlan
BINARIES := yanet-cli $(addprefix yanet-cli-,$(MODULES))
TARGETS := $(addprefix build/,$(BINARIES))

build: $(TARGETS)

build/%:
	$(CARGO) build --release --package $*

install:
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(addprefix ../target/release/,$(BINARIES)) $(DESTDIR)$(BINDIR)/

clean:
	$(CARGO) clean

.PHONY: build install clean
