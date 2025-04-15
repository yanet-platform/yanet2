# Commands
GO := go
PROTOC := protoc
MESON := meson
NINJA := ninja

# Directories
ROOT_DIR := ../..
BUILD_DIR := $(ROOT_DIR)/build
PROTO_DIR := controlplane/nat64pb
CLI_DIR := cli
DATAPLANE_DIR := dataplane
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

# Output binary
OUTPUT_BIN := $(BUILD_DIR)/modules/nat64/nat64
CLI_OUTPUT_DIR := $(BUILD_DIR)/modules/nat64/cli
DATAPLANE_OUTPUT_DIR := $(BUILD_DIR)/modules/nat64

# Proto file dependencies
PROTO_FILES := $(PROTO_DIR)/nat64.proto

# Protoc options
PROTOC_GO_OPT := --go_opt=paths=source_relative
PROTOC_GRPC_OPT := --go-grpc_opt=paths=source_relative

.PHONY: proto cli controlplane dataplane all

all: cli controlplane dataplane

cli:
	$(MAKE) -C $(CLI_DIR)

controlplane: proto
	$(GO) build -o $(OUTPUT_BIN) ./controlplane

dataplane:
	@echo "+ Building nat64 module dataplane"
	@cd $(ROOT_DIR) && $(NINJA) -C build modules/nat64/dataplane/libnat64_dp.a modules/nat64/libnat64_cp.a
	@echo "+ Dataplane building completed"

proto: $(PROTO_FILES)
	@echo "+ Generating protobuf code for nat64 module"
	@if ! which $(PROTOC) > /dev/null; then echo "protoc protobuf compiler required for build"; exit 1; fi;
	@$(PROTOC) -I ./$(PROTO_DIR) --go_out=./$(PROTO_DIR) $(PROTOC_GO_OPT) --go-grpc_out=./$(PROTO_DIR) $(PROTOC_GRPC_OPT) $(PROTO_FILES)
