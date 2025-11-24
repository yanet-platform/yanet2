# Makefile for automating yanet1 → yanet2 test migration

# Variables
# YANET1_ROOT can be set as environment variable or Makefile variable
# If not set, defaults to relative path ../../../yanet1
YANET1_ROOT_REL ?= ../../../yanet1
YANET1_ROOT ?= $(abspath $(YANET1_ROOT_REL))
YANET2_DIR_REL ?= ../..
YANET2_DIR = $(abspath $(YANET2_DIR_REL))
CONVERTER_DIR = converter
OUTPUT_DIR_REL = $(YANET2_DIR)/tests/functional/converted
OUTPUT_DIR = $(abspath $(OUTPUT_DIR_REL))
SKIPLIST ?= $(abspath $(CONVERTER_DIR)/../skiplist.yaml)
EXAMPLES_DIR = examples

# Default targets
.PHONY: all build convert clean help test convert-one analyze-pcaps check-deps stats validate integrate report quickstart distclean install examples tidy

all: build convert

help:
	@echo "Available commands:"
	@echo "  build     - Build test converter"
	@echo "  convert   - Convert all yanet1 tests"
	@echo "  convert-one TEST=<name> - Convert single test"
	@echo "  test      - Run converter tests"
	@echo "  clean     - Clean generated files"
	@echo "  install   - Install converter to PATH"
	@echo "  examples  - Create migration examples"
	@echo ""
	@echo "Variables:"
	@echo "  YANET1_ROOT - Path to yanet1 directory (default: ../../../yanet1)"
	@echo "  YANET2_DIR_REL - Relative path to yanet2 (default: ../..)"
	@echo "  OUTPUT_DIR - Output directory (default: yanet2/tests/functional/converted)"
 	@echo "  SKIPLIST  - Path to skiplist.yaml (default: converter/../skiplist.yaml if exists)"

# Tidy go.mod dependencies
tidy:
	@echo "Tidying Go modules..."
	cd $(CONVERTER_DIR) && go mod tidy

# Build converter
build: tidy
	@echo "Building test converter..."
	cd $(CONVERTER_DIR) && go build -o yanet-test-converter ./main.go
	@echo "Converter built: $(CONVERTER_DIR)/yanet-test-converter"

# Convert all tests
convert: build
	@echo "Converting all tests from $(YANET1_ROOT)/autotest/units..."
	@mkdir -p $(OUTPUT_DIR)
	./$(CONVERTER_DIR)/yanet-test-converter \
		-input $(YANET1_ROOT)/autotest/units \
		-output $(OUTPUT_DIR) \
		-batch \
		-skiplist $(SKIPLIST) \
		-v
	@echo "Tests converted to $(OUTPUT_DIR)/"

# Convert single test
# Usage: make convert-one TEST=016_route
# or:    make convert-one TEST=001_one_port/016_route (full path)
convert-one: build
	@if [ -z "$(TEST)" ]; then \
		echo "Error: specify TEST=<test_name>"; \
		echo "Example: make convert-one TEST=016_route"; \
		echo "     or: make convert-one TEST=001_one_port/016_route"; \
		exit 1; \
	fi
	@echo "Converting test $(TEST)..."
	@mkdir -p $(OUTPUT_DIR)
	@TEST_PATH=$$(find $(YANET1_ROOT)/autotest/units -type d -name "$(TEST)" | head -1); \
	if [ -z "$$TEST_PATH" ]; then \
		echo "Error: Test $(TEST) not found in $(YANET1_ROOT)/autotest/units"; \
		exit 1; \
	fi; \
	TEST_NAME=$$(basename $$TEST_PATH); \
	TEST_ABS=$$(cd $$TEST_PATH && pwd); \
	echo "Found test at: $$TEST_PATH"; \
	./$(CONVERTER_DIR)/yanet-test-converter \
		-input $$TEST_ABS \
		-output $(OUTPUT_DIR) \
		-test $$TEST_NAME \
		-skiplist $(SKIPLIST) \
		-v
	@echo "Test $(TEST) converted to $(OUTPUT_DIR)/"

# Test converter
test: build
	@echo "Running converter tests..."
	cd $(CONVERTER_DIR) && go test -v ./...

# Clean
clean:
	@echo "Cleaning generated files..."
	rm -f $(OUTPUT_DIR)/[0-9]*_test.go
	rm -f $(CONVERTER_DIR)/yanet-test-converter
	cd $(CONVERTER_DIR) && go clean

# Analyze pcap files in yanet1 tests
analyze-pcaps:
	@echo "Analyzing pcap files in yanet1 tests..."
	@find $(YANET1_ROOT)/autotest/units -name "*.pcap" -exec echo "=== {} ===" \; -exec tcpdump -r {} -c 1 -v \; 2>/dev/null || echo "tcpdump not found"

# Check dependencies
check-deps:
	@echo "Checking dependencies..."
	@which go >/dev/null || (echo "Error: Go not installed" && exit 1)
	@which tcpdump >/dev/null || echo "Warning: tcpdump not found (optional)"
	@[ -d "$(YANET1_ROOT)" ] || (echo "Error: yanet1 not found in $(YANET1_ROOT)" && exit 1)
	@[ -d "$(YANET2_DIR)" ] || (echo "Error: yanet2 not found in $(YANET2_DIR)" && exit 1)
	@echo "All dependencies OK"

# Statistics of yanet1 tests
stats:
	@echo "Statistics of yanet1 tests:"
	@echo "Total test directories: $$(find $(YANET1_ROOT)/autotest/units -type d -name "*_*" | wc -l)"
	@echo "autotest.yaml files: $$(find $(YANET1_ROOT)/autotest/units -name "autotest.yaml" | wc -l)"
	@echo "controlplane.conf files: $$(find $(YANET1_ROOT)/autotest/units -name "controlplane.conf" | wc -l)"
	@echo "PCAP files: $$(find $(YANET1_ROOT)/autotest/units -name "*.pcap" | wc -l)"
	@echo ""
	@echo "Test types:"
	@find $(YANET1_ROOT)/autotest/units -type d -name "*_*" | sed 's/.*\///' | cut -d'_' -f3- | sort | uniq -c | sort -nr

# Validate generated tests
validate: convert
	@echo "Validating generated tests..."
	@if [ -d "$(OUTPUT_DIR)" ] && [ -n "$$(ls -A $(OUTPUT_DIR)/*.go 2>/dev/null)" ]; then \
		echo "Running gofmt..."; \
		gofmt -s -w $(OUTPUT_DIR)/*.go || echo "Warning: gofmt failed"; \
		echo "Running go vet..."; \
		cd $(OUTPUT_DIR) && go vet . 2>/dev/null || echo "Note: go vet found issues (expected for partial tests)"; \
	else \
		echo "No generated tests to validate"; \
	fi
	@echo "Validation completed"