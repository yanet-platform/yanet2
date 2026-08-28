# Nix development workflow

Install Nix once by following the [official installation guide](https://nixos.org/download/).
Install the stable Rust toolchain, Clippy, rust-analyzer, and nightly rustfmt
with [rustup](https://rustup.rs/):

```bash
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
source "$HOME/.cargo/env"
rustup toolchain install stable --component clippy --component rust-analyzer
rustup default stable
rustup toolchain install nightly --component rustfmt
```

The installer adds Cargo to `PATH` for future login shells. Source
`$HOME/.cargo/env` as above, or log in again before running `rustup`.

Rust is intentionally kept outside the Nix shell so that normal rustup commands
such as `cargo +nightly fmt` work unchanged. Then enter the project environment
from the repository root:

```bash
nix develop
```

The first invocation downloads the versions pinned by `flake.lock`. Inside the
shell, initialize and configure the project once:

```bash
git submodule update --init --recursive
meson setup build
```

GCC is the default compiler, matching CI. Clang 19 is also available; use a
separate build directory because Meson fixes the compiler at setup time:

```bash
CC=clang CXX=clang++ meson setup build-clang
meson compile -C build-clang
meson test -C build-clang
```

Use the regular project commands afterwards, for example:

```bash
meson compile -C build
make test
cargo build --workspace
npm ci
npm run build -w web
```

Go itself comes from Nix, but its module cache and installed tools remain in
the normal writable user directories. `go install example.org/tool@version`
writes the binary to `$GOBIN`, or to `$(go env GOPATH)/bin` when `$GOBIN` is
unset. Add that directory to the host `PATH` if it is not already there. A tool
also listed in `flake.nix` takes precedence inside `nix develop`.

Run a single command without entering an interactive shell when convenient:

```bash
nix develop --command meson compile -C build
nix develop --command make test
```

Leave an interactive development shell with `exit`. The tools are not installed
globally; they remain in `/nix/store` and become available through `nix develop`.
`nix store gc` may remove an unused development-shell closure, in which case the
next `nix develop` downloads it again.

QEMU is included in the shell. KVM is optional acceleration; the functional
tests fall back to QEMU TCG when `/dev/kvm` is unavailable. To use KVM, grant
access on the Linux host:

```bash
sudo usermod -aG kvm "$(id -un)"
test -r /dev/kvm && test -w /dev/kvm
```

Log in again after changing group membership. Functional tests configure
hugepages inside the guest; host hugepages and other DPDK settings are needed
when running the dataplane directly on the host.

On x86-64, build the release artifacts with `make all`, then run
`make test-functional`. The test harness stages Nix-built executables so they
run with the guest system loader; the guest does not need `/nix/store`.
Functional tests from aarch64 hosts are not yet supported.
