.PHONY: ynpb build modules/route/routepb

# Commands
GO := go
PROTOC := protoc

# Directories
BUILD_DIR := ../build
YNPB_DIR := ynpb
ROUTE_PB_DIR := modules/route/routepb

# Output binary
OUTPUT_BIN := $(BUILD_DIR)/yanet-controlplane
MAIN_GO := cmd/yncp-director/main.go

# Proto file dependencies
YNPB_PROTO_FILES := \
	$(YNPB_DIR)/gateway.proto \
	$(YNPB_DIR)/logging.proto \
	$(YNPB_DIR)/inspect.proto
ROUTE_PROTO_FILES := $(ROUTE_PB_DIR)/route.proto $(ROUTE_PB_DIR)/neighbour.proto $(ROUTE_PB_DIR)/macaddr.proto

# Protoc options
PROTOC_GO_OPT := --go_opt=paths=source_relative
PROTOC_GRPC_OPT := --go-grpc_opt=paths=source_relative

all: build

$(YNPB_DIR): $(YNPB_PROTO_FILES)
	@echo "+ $@"
	$(PROTOC) -I ./$(YNPB_DIR) --go_out=./$@ $(PROTOC_GO_OPT) --go-grpc_out=./$@ $(PROTOC_GRPC_OPT) $^

$(ROUTE_PB_DIR): $(ROUTE_PROTO_FILES)
	@echo "+ $@"
	@if ! which $(PROTOC) > /dev/null; then echo "protoc protobuf compiler required for build"; exit 1; fi;
	@$(PROTOC) -I ./$(ROUTE_PB_DIR) --go_out=./$@ $(PROTOC_GO_OPT) --go-grpc_out=./$@ $(PROTOC_GRPC_OPT) $(ROUTE_PROTO_FILES)

build: ynpb $(ROUTE_PB_DIR)
	$(GO) build -o $(OUTPUT_BIN) $(MAIN_GO)
