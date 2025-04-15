# Commands
GO := go
PROTOC := protoc
MESON := meson
NINJA := ninja

# Directories
ROOT_DIR := ../..
BUILD_DIR := $(ROOT_DIR)/build
PROTO_DIR := controlplane/forwardpb
CLI_DIR := cli
DATAPLANE_DIR := dataplane
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

# Output binary
OUTPUT_BIN := $(BUILD_DIR)/modules/forward/forward
CLI_OUTPUT_DIR := $(BUILD_DIR)/modules/forward/cli
DATAPLANE_OUTPUT_DIR := $(BUILD_DIR)/modules/forward

# Proto file dependencies
PROTO_FILES := $(PROTO_DIR)/forward.proto

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
	@echo "+ Building forward module dataplane"
	@cd $(ROOT_DIR) && $(NINJA) -C build modules/forward/dataplane/libforward_dp.a modules/forward/libforward_cp.a
	@echo "+ Dataplane building completed"

proto: $(PROTO_FILES)
	@echo "+ Generating protobuf code for forward module"
	@if ! which $(PROTOC) > /dev/null; then echo "protoc protobuf compiler required for build"; exit 1; fi;
	@$(PROTOC) -I ./$(PROTO_DIR) --go_out=./$(PROTO_DIR) $(PROTOC_GO_OPT) --go-grpc_out=./$(PROTO_DIR) $(PROTOC_GRPC_OPT) $(PROTO_FILES)
