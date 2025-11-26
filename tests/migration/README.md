# Migration of yanet1 autotests → yanet2

## Overview

The converter generates Go-based functional tests from yanet1 autotests (YAML + PCAP + optional gen.py).

- By default the yanet1 tree is expected to be checked out next to `yanet2`, e.g.:
  - `.../projects/yanet/yanet1`
  - `.../projects/yanet/yanet2`
- Functional Makefile uses `YANET1_ROOT` (default `../../../../yanet1` from `tests/functional`) and expects tests under `$(YANET1_ROOT)/autotest/units/001_one_port/`.

## How to use the converter

- **Recommended entrypoint (from functional tests)**
  - Work in `yanet2/tests/functional` and use the Makefile targets:
    - Convert one test:
      ```bash
      cd yanet2/tests/functional
      make convert-one TEST=061_nat64stateful
      ```
    - Convert all tests:
      ```bash
      make convert
      ```
    - Run converted tests (optionally with `TEST=...` filter):
      ```bash
      make test-converted
      ```
  - If your yanet1 checkout is in a non-standard location, override the default:
    ```bash
    make convert-one TEST=061_nat64stateful YANET1_ROOT=/absolute/path/to/yanet1
    ```

- **Direct usage (advanced)**
  - Use `yanet2/tests/migration` as the working directory.
  - Converter entrypoint: `converter/main.go`.
  - Build binary:
    ```bash
    cd yanet2/tests/migration
    go build -o yanet-test-converter ./converter
    ```
  - Or run without build:
    ```bash
    cd yanet2/tests/migration
    go run ./converter [...]
    ```

## How to convert and run a test

- **Step 1. Choose source test**
  - yanet1 tests live under `yanet1/autotest/units/001_one_port/<test_name>`.
  - Example: `061_nat64stateful`.

- **Step 2. Convert from functional tests**
  ```bash
  cd yanet2/tests/functional
  make convert-one TEST=061_nat64stateful
  ```

- **Step 3. Run converted Go test**
  ```bash
  cd yanet2/tests/functional
  make test-converted TEST=Test061_nat64stateful
  ```

## How to control converter behaviour

- **Core flags**
  - `-input`: path to yanet1 tests (usually `.../autotest/units/001_one_port`).
  - `-output`: where to put generated Go tests (e.g. `../functional/converted`).
  - `-test` / `-batch`: single test vs all tests in a directory.
  - `-v` / `-debug`: verbose logs; `-debug` also enables detailed converter internals.

- **AST vs PCAP**
  - Default: use the PCAP analyzer for all tests.
  - `-force-ast`: require AST parser and use the AST-based pipeline (fail if parser is missing).

- **Strict vs tolerant mode**
  - `-strict`: fail on unsupported layers/special handling and missing AST parser (good for CI).
  - `-tolerant` (default): log warnings, continue with best-effort conversion and PCAP fallback.

- **Skiplist (`skiplist.yaml`)**
  - Per-test and per-step states: `enabled`, `wovlan`, `disabled`.
  - Global `"*"` entry defines defaults for tests not explicitly listed.
  - Auto-generated section is below:
    ```yaml
    # ---- Auto-generated entries below (do not edit) ----
    ```
  - Converter can refresh the auto-generated part:
    ```bash
    go run ./converter \
    -input ../../../yanet1/autotest/units \
    -skiplist ../skiplist.yaml \
    -update-skiplist
    ```

- **Packet validation (internal tests)**
  - Raw mode: byte-for-byte comparison of generated vs original PCAP.
  - Layer-aware mode: compares semantic fields per layer and tolerates Ethernet padding and checksum-only differences.

## How to make changes

- **Core files**
  - `converter/lib/converter.go`: main orchestration (YAML parsing, PCAP/AST selection, step conversion, test templates).
  - `converter/lib/pcap_utils.go`: PCAP → IR, including IPv6, GRE, malformed IPv4/TCP.
  - `converter/lib/packet_builder.go`: runtime packet builders used in generated tests.
  - `converter/lib/cli_parser.go`: yanet1 CLI parsing and yanet2 CLI formatting.
  - `converter/lib/validation.go`: packet validator helpers for internal tests.

- **Workflow**
  ```bash
  cd yanet2/tests/migration/converter
  go test ./lib/...
  ```
  - After changes: re-convert one or two representative tests and re-run their Go tests to ensure behaviour is unchanged.
