#!/bin/bash
set -euo pipefail
[[ $(uname -m) == aarch64 ]]
root=$(git rev-parse --show-toplevel)
cd "$root/scripts/bpftime/cp-lock"
source_dir=$PWD/.deps/upstream
[[ $(git -C "$source_dir" rev-parse HEAD) == 6bde654abce3649fe8e6943b16419dfdf71ebf38 ]]
git -C "$source_dir" diff --exit-code
if git -C "$source_dir" submodule status --recursive | grep -qE '^[+U-]'; then
    echo 'upstream submodules do not match the pinned revision' >&2
    exit 1
fi

cmake -S "$source_dir" -B "$source_dir/build" \
    -DCMAKE_BUILD_TYPE=RelWithDebInfo -DBUILD_BPFTIME_DAEMON=OFF \
    -DBPFTIME_UBPF_JIT=OFF -DBPFTIME_LLVM_JIT=ON \
    -DCMAKE_C_COMPILER=clang-16 -DCMAKE_CXX_COMPILER=clang++-16 \
    -DLLVM_CONFIG=/usr/bin/llvm-config-16 \
    -DLLVM_DIR=/usr/lib/llvm-16/lib/cmake/llvm
cmake --build "$source_dir/build" --parallel 2 \
    --target bpftime-cli-cpp bpftime-agent bpftime-syscall-server

mkdir -p .deps/runtime .deps/notices/runtime
install -m755 "$source_dir/build/tools/cli/bpftime" .deps/runtime/
install -m755 "$source_dir/build/runtime/agent/libbpftime-agent.so" .deps/runtime/
install -m755 "$source_dir/build/runtime/syscall-server/libbpftime-syscall-server.so" .deps/runtime/
for notice in LICENSE:bpftime third_party/spdlog/LICENSE:spdlog third_party/argparse/LICENSE:argparse vm/llvm-jit/LICENSE:llvmbpf; do
    cp "$source_dir/${notice%:*}" ".deps/notices/runtime/${notice#*:}"
done
cp /usr/share/doc/llvm-16/copyright .deps/notices/runtime/LLVM
cp "$source_dir/third_party/bpftool/libbpf/LICENSE.BSD-2-Clause" .deps/notices/runtime/libbpf
cat "$root/LICENSE" .deps/notices/runtime/{bpftime,llvmbpf,spdlog,argparse,LLVM,libbpf} > .deps/copyright
(
    cd .deps/runtime
    sha256sum bpftime libbpftime-agent.so libbpftime-syscall-server.so > SHA256SUMS
    printf 'v0.9.0-arm64@sha256:%s\n' "$(sha256sum SHA256SUMS | cut -d' ' -f1)" > BUILD
)

CC=clang-19 meson setup "$root/build" "$root" -Ddataplane_only=true
make selftest
tar -czf .deps/bpftime-arm64.tar.gz -C .deps runtime notices/runtime
(cd .deps && sha256sum bpftime-arm64.tar.gz > bpftime-arm64.tar.gz.sha256)
