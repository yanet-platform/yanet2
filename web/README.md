# YANET2 Web UI

Web interface for the YANET2 router platform.

## Overview

This web UI provides a browser-based interface for interacting with the YANET2 controlplane API.

## Development Setup

### Prerequisites

- Rust toolchain (1.84 or newer)
- wasm-bindgen-cli
- Trunk (Rust WebAssembly build tool)

### Install Dependencies

```bash
# Install Rust (if not already installed)
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh

# Add WebAssembly target
rustup target add wasm32-unknown-unknown

# Install Trunk
cargo install trunk

# Install wasm-bindgen-cli
cargo install wasm-bindgen-cli --version 0.2.100
```

### Building and Running Locally

1. Clone the repository:
   ```bash
   git clone https://github.com/yanet-platform/yanet2.git
   cd yanet2/web
   ```

2. Run the development server:
   ```bash
   trunk serve
   ```

3. Access the UI in your browser at http://127.0.0.1:8080

### Building for Production

To build an optimized version:

```bash
trunk build --release
```

The built files will be available in the `dist` directory.
