.DEFAULT_GOAL := all
CC := $(shell command -v clang-19 || command -v clang-15 || command -v clang)
GO ?= go
PYTHON ?= python3
BPF_CLANG ?= $(CC)
LIBBPF_CFLAGS = $(shell pkg-config --cflags libbpf)
LIBBPF_LIBS = $(shell pkg-config --libs libbpf)
CFLAGS ?= -O2 -g -Wall -Wextra
HOST_ARCH := $(shell uname -m)
ifeq ($(HOST_ARCH),x86_64)
BPF_ARCH := x86
DPDK_ARCH := x86
else ifeq ($(HOST_ARCH),aarch64)
BPF_ARCH := arm64
DPDK_ARCH := arm
CFLAGS += -march=armv8-a+crc
else
$(error unsupported yanet-uprobe architecture: $(HOST_ARCH))
endif

.PHONY: all prepare selftest install clean
prepare:
	./prepare.sh
all: cp_lock.bpf.o setup yanet-uprobe
	ln -sfn $(CURDIR)/.deps/runtime bpftime
cp_lock.bpf.o: cp_lock.bpf.c cp_lock.h
	$(BPF_CLANG) -O2 -g -target bpf -mcpu=v3 -D__TARGET_ARCH_$(BPF_ARCH) $(LIBBPF_CFLAGS) -I/usr/include/$(HOST_ARCH)-linux-gnu -c $< -o $@
setup: setup.c
	$(CC) $(CPPFLAGS) $(CFLAGS) $(LIBBPF_CFLAGS) $< $(LDFLAGS) $(LIBBPF_LIBS) -o $@
REPO := $(abspath ../..)
CGO_BUILD_ENV := CC="$(CC)" CGO_CPPFLAGS='$(CPPFLAGS)' CGO_CFLAGS='$(CFLAGS)' CGO_LDFLAGS='$(LDFLAGS)'
GO_LDFLAGS = -s=false -w=false -X main.runtimeLabel=$(shell cat .deps/runtime/BUILD)
yanet-uprobe: main.go support.go adapter.c adapter.h cp_lock.h .deps/runtime/BUILD $(REPO)/go.mod $(REPO)/go.sum
	$(CGO_BUILD_ENV) $(GO) build -tags yanet_uprobe -ldflags='$(GO_LDFLAGS)' -o $@ .
DPDK_INCLUDES := $(addprefix -I,$(REPO)/build/subprojects/dpdk $(wildcard $(REPO)/subprojects/dpdk/lib/*) $(REPO)/subprojects/dpdk/config $(REPO)/subprojects/dpdk/lib/eal/include $(REPO)/subprojects/dpdk/lib/eal/linux/include $(REPO)/subprojects/dpdk/lib/eal/$(DPDK_ARCH)/include)
selftest/zone.o: ../../lib/controlplane/config/zone.c Makefile selftest/zone.d
	$(CC) $(CFLAGS) -MMD -MP -MF selftest/zone.d -MT $@ -fPIC -ffunction-sections -fdata-sections -D_GNU_SOURCE -include rte_config.h $(DPDK_INCLUDES) -I../.. -c $< -o $@.full
	ld -r --gc-sections -u cp_config_lock -u cp_config_try_lock -u cp_config_unlock $@.full -o $@
	rm $@.full
selftest/zone.d:
-include selftest/zone.d
selftest: all selftest/zone.o
	$(CGO_BUILD_ENV) $(GO) test -count=1 -ldflags='$(GO_LDFLAGS)' -tags yanet_uprobe .
	@mkdir -p .deps
	GOFLAGS="$$($(GO) env GOFLAGS) -tags=yanet_uprobe" CC="$(CC)" $(PYTHON) $(REPO)/scripts/go_cdeps.py \
		--root "$(REPO)" --go "$(GO)" --package scripts/uprobe/selftest \
		--build-tag yanet_uprobe --output "$(CURDIR)/selftest/zz_yanet_cdeps.c" \
		--stamp "$(CURDIR)/.deps/selftest-go-cdeps.stamp" "$(CURDIR)/selftest/zone.o"
	YANET_UPROBE_HELPER_DIR=$(CURDIR) CC="$(CC)" $(GO) test -count=1 -ldflags='-s=false -w=false' -tags yanet_uprobe ./selftest
install: all
	install -Dm755 yanet-uprobe $(DESTDIR)/usr/bin/yanet-uprobe
	install -Dm755 setup $(DESTDIR)/usr/lib/yanet2/uprobe/setup
	install -Dm644 cp_lock.bpf.o $(DESTDIR)/usr/lib/yanet2/uprobe/cp_lock.bpf.o
	install -d $(DESTDIR)/usr/share/doc/yanet2-uprobe
	install -d $(DESTDIR)/usr/lib/yanet2/uprobe/bpftime
	for binary in bpftime libbpftime-agent.so libbpftime-syscall-server.so; do install -m755 $(CURDIR)/.deps/runtime/$$binary $(DESTDIR)/usr/lib/yanet2/uprobe/bpftime/; done
	cp $(CURDIR)/.deps/runtime/BUILD README.md $(DESTDIR)/usr/share/doc/yanet2-uprobe/
	install -m644 .deps/copyright $(DESTDIR)/usr/share/doc/yanet2-uprobe/copyright
clean:
	rm -f setup yanet-uprobe cp_lock.bpf.o selftest/zone.o selftest/zone.o.full selftest/zone.d selftest/zz_yanet_cdeps.c .deps/selftest-go-cdeps.stamp
