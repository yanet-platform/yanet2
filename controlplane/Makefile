# Commands
GO := go
PROTOC := protoc

# Directories
BUILD_DIR := ../build
YNPB_DIR := ynpb
FORWARD_PB_DIR := modules/forward/forwardpb
NAT64_PB_DIR := modules/nat64/nat64pb

# Output binary
OUTPUT_BIN := $(BUILD_DIR)/yanet-controlplane
MAIN_GO := cmd/yncp-director/main.go

# Proto file dependencies
YNPB_PROTO_FILES := \
	$(YNPB_DIR)/gateway.proto \
	$(YNPB_DIR)/logging.proto \
	$(YNPB_DIR)/inspect.proto \
	$(YNPB_DIR)/pipeline.proto
FORWARD_PROTO_FILES := $(FORWARD_PB_DIR)/forward.proto
NAT64_PROTO_FILES := $(NAT64_PB_DIR)/nat64.proto

# Protoc options
PROTOC_GO_OPT := --go_opt=paths=source_relative
PROTOC_GRPC_OPT := --go-grpc_opt=paths=source_relative

.PHONY: ynpb build $(FORWARD_PB_DIR) $(NAT64_PB_DIR)


all: build

$(YNPB_DIR): $(YNPB_PROTO_FILES)
	@echo "+ $@"
	$(PROTOC) -I ./$(YNPB_DIR) --go_out=./$@ $(PROTOC_GO_OPT) --go-grpc_out=./$@ $(PROTOC_GRPC_OPT) $^

$(FORWARD_PB_DIR): $(FORWARD_PROTO_FILES)
	@echo "+ $@"
	@if ! which $(PROTOC) > /dev/null; then echo "protoc protobuf compiler required for build"; exit 1; fi;
	@$(PROTOC) -I ./$(FORWARD_PB_DIR) --go_out=./$@ $(PROTOC_GO_OPT) --go-grpc_out=./$@ $(PROTOC_GRPC_OPT) $(FORWARD_PROTO_FILES)

$(NAT64_PB_DIR): $(NAT64_PROTO_FILES)
	@echo "+ $@"
	@if ! which $(PROTOC) > /dev/null; then echo "protoc protobuf compiler required for build"; exit 1; fi;
	@$(PROTOC) -I ./$(NAT64_PB_DIR) --go_out=./$@ $(PROTOC_GO_OPT) --go-grpc_out=./$@ $(PROTOC_GRPC_OPT) $(NAT64_PROTO_FILES)

build: ynpb $(FORWARD_PB_DIR) $(NAT64_PB_DIR)
	$(GO) build -o $(OUTPUT_BIN) $(MAIN_GO)
