# Commands
GO := go
PROTOC := protoc

# Directories
BUILD_DIR := ../build
YNPB_DIR := ynpb

# Output binary
OUTPUT_BIN := $(BUILD_DIR)/yanet-controlplane
MAIN_GO := cmd/yncp-director/main.go

# Proto file dependencies
YNPB_PROTO_FILES := \
	$(YNPB_DIR)/gateway.proto \
	$(YNPB_DIR)/logging.proto \
	$(YNPB_DIR)/inspect.proto \
	$(YNPB_DIR)/pipeline.proto

# Protoc options
PROTOC_GO_OPT := --go_opt=paths=source_relative
PROTOC_GRPC_OPT := --go-grpc_opt=paths=source_relative

.PHONY: ynpb build

all: build

$(YNPB_DIR): $(YNPB_PROTO_FILES)
	@echo "+ $@"
	$(PROTOC) -I ./$(YNPB_DIR) --go_out=./$@ $(PROTOC_GO_OPT) --go-grpc_out=./$@ $(PROTOC_GRPC_OPT) $^


build: ynpb
	$(GO) build -o $(OUTPUT_BIN) $(MAIN_GO)
