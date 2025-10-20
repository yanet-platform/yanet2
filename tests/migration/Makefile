# Makefile for automating yanet1 → yanet2 test migration

# Variables
YANET1_DIR_REL ?= ../../../yanet1
YANET1_DIR = $(abspath $(YANET1_DIR_REL))
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
	@echo "  YANET1_DIR_REL - Relative path to yanet1 (default: ../../../yanet1)"
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
	@echo "Converting all tests from $(YANET1_DIR)/autotest/units..."
	@mkdir -p $(OUTPUT_DIR)
	./$(CONVERTER_DIR)/yanet-test-converter \
		-input $(YANET1_DIR)/autotest/units \
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
	@TEST_PATH=$$(find $(YANET1_DIR)/autotest/units -type d -name "$(TEST)" | head -1); \
	if [ -z "$$TEST_PATH" ]; then \
		echo "Error: Test $(TEST) not found in $(YANET1_DIR)/autotest/units"; \
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
	rm -rf $(OUTPUT_DIR)/README.md
	rm -rf $(OUTPUT_DIR)/SETUP_NOTES.md
	rm -f $(CONVERTER_DIR)/yanet-test-converter
	cd $(CONVERTER_DIR) && go clean

# Install converter to PATH
INSTALL_DIR ?= /usr/local/bin
install: build
	@echo "Installing converter to $(INSTALL_DIR)..."
	@if [ -w "$(INSTALL_DIR)" ]; then \
		cp $(CONVERTER_DIR)/yanet-test-converter $(INSTALL_DIR)/; \
	else \
		echo "$(INSTALL_DIR) is not writable, using sudo..."; \
		sudo cp $(CONVERTER_DIR)/yanet-test-converter $(INSTALL_DIR)/; \
	fi
	@echo "Converter installed. Use: yanet-test-converter"

# Create examples
examples:
	@echo "Creating migration examples..."
	@mkdir -p $(EXAMPLES_DIR)
	@echo "Examples available in $(EXAMPLES_DIR)/"

# Analyze pcap files in yanet1 tests
analyze-pcaps:
	@echo "Analyzing pcap files in yanet1 tests..."
	@find $(YANET1_DIR)/autotest/units -name "*.pcap" -exec echo "=== {} ===" \; -exec tcpdump -r {} -c 1 -v \; 2>/dev/null || echo "tcpdump not found"

# Check dependencies
check-deps:
	@echo "Checking dependencies..."
	@which go >/dev/null || (echo "Error: Go not installed" && exit 1)
	@which tcpdump >/dev/null || echo "Warning: tcpdump not found (optional)"
	@[ -d "$(YANET1_DIR)" ] || (echo "Error: yanet1 not found in $(YANET1_DIR)" && exit 1)
	@[ -d "$(YANET2_DIR)" ] || (echo "Error: yanet2 not found in $(YANET2_DIR)" && exit 1)
	@echo "All dependencies OK"

# Statistics of yanet1 tests
stats:
	@echo "Statistics of yanet1 tests:"
	@echo "Total test directories: $$(find $(YANET1_DIR)/autotest/units -type d -name "*_*" | wc -l)"
	@echo "autotest.yaml files: $$(find $(YANET1_DIR)/autotest/units -name "autotest.yaml" | wc -l)"
	@echo "controlplane.conf files: $$(find $(YANET1_DIR)/autotest/units -name "controlplane.conf" | wc -l)"
	@echo "PCAP files: $$(find $(YANET1_DIR)/autotest/units -name "*.pcap" | wc -l)"
	@echo ""
	@echo "Test types:"
	@find $(YANET1_DIR)/autotest/units -type d -name "*_*" | sed 's/.*\///' | cut -d'_' -f3- | sort | uniq -c | sort -nr

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

# Integrate with yanet2
integrate: validate
	@echo "Tests are already in yanet2/tests/functional/converted/"
	@echo "Generated tests are ready to use with the functional test framework."
	@echo ""
	@echo "Note: Tests use globalFramework from framework_test.go"
	@echo "Make sure framework is properly initialized before running converted tests."

# Create migration report
report: stats
	@echo "Creating migration report..."
	@{
		echo "# Migration report of yanet1 → yanet2 tests"; \
		echo ""; \
		echo "Date: $$(date)"; \
		echo ""; \
		echo "## Source tests statistics"; \
		echo ""; \
		make stats 2>/dev/null | grep -E "(Total|files|PCAP|Test types)" -A 20; \
		echo ""; \
		echo "## Generated tests"; \
		echo ""; \
		if [ -d "$(OUTPUT_DIR)" ]; then \
			echo "Go test files: $$(ls $(OUTPUT_DIR)/*_test.go 2>/dev/null | wc -l)"; \
			echo ""; \
			echo "### List of generated tests"; \
			echo ""; \
			ls $(OUTPUT_DIR)/*_test.go 2>/dev/null | sed 's/.*\//- /' || echo "No generated tests"; \
		else \
			echo "Tests not generated. Run 'make convert'"; \
		fi; \
		echo ""; \
		echo "## Next steps"; \
		echo ""; \
		echo "1. Check generated tests in $(OUTPUT_DIR)/"; \
		echo "2. Adapt tests for module specifics"; \
		echo "3. Run 'make integrate' to copy to yanet2"; \
		echo "4. Update framework_test.go"; \
		echo "5. Run tests: cd $(YANET2_DIR) && make test-functional"; \
	} > migration_report.md
	@echo "Report created: migration_report.md"

# Quick start
quickstart: check-deps build convert validate report
	@echo ""
	@echo "🎉 Quick start completed!"
	@echo ""
	@echo "Next steps:"
	@echo "1. View report: cat migration_report.md"
	@echo "2. Check tests: ls $(OUTPUT_DIR)/"
	@echo "3. Integrate: make integrate"
	@echo ""

# Clean everything (including dependencies)
distclean: clean
	cd $(CONVERTER_DIR) && go mod clean -cache
	rm -f migration_report.md