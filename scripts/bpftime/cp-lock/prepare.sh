#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
GOTOOLCHAIN=${GOTOOLCHAIN:-go1.27.1} go version
case $(uname -m) in
x86_64)
    image=ghcr.io/eunomia-bpf/bpftime@sha256:6e315ea561d08e482159385a4775ee6564fc4a6f416f24ae40bfccb176191ed9
    identity=v0.9.0@sha256:6e315ea561d08e482159385a4775ee6564fc4a6f416f24ae40bfccb176191ed9
    hashes='d7ce69e5f1b1fc6d55483964a54c5d98588df4978b3dd0042208e4c84dadd2b5  bpftime
324ec32e22942c41d7d5725d5c27907d8ba60f29b2bfbf62d9725fcbc6cff027  libbpftime-agent.so
025a480229372eebf8b552aa2d718d9b72b4d20676f61ba0073801bb42be6955  libbpftime-syscall-server.so'
    ;;
aarch64)
    identity=v0.9.0-arm64@sha256:f5d0287b32dd8c933036e553eab36a8d067a152a7d99536123b6005c3f8c960f
    archive=https://github.com/yanet-platform/yanet2/releases/download/bpftime-v0.9.0-arm64-37632565870-1/bpftime-arm64.tar.gz
    archive_hash=c2d7202f814bc735928f5bbf17f155ceb8db01b82ce0e7e4b40fc94e3ed9b77c
    hashes='c5ee2e55700417889acdce881b7fce842a73fea983686ee955786ab2a412f18d  bpftime
b6bbff93881f7e2093bae25df27b64ae714948914a8c136a4891591a7b019529  libbpftime-agent.so
2adb1636a660644d2dd5f3b6953c2c5ab182c170846bea786bf7789067f0e0ad  libbpftime-syscall-server.so'
    ;;
*) echo 'cp-lock supports amd64 and arm64 only' >&2; exit 1 ;;
esac

verify_runtime() (
    cd .deps/runtime
    sha256sum --check --status <<< "$hashes"
)

assemble_notices() {
    for notice in .deps/notices/runtime/{bpftime,llvmbpf,spdlog,argparse,LLVM,libbpf}; do
        [[ -s $notice ]] || return 1
    done
    cat ../../../LICENSE .deps/notices/runtime/{bpftime,llvmbpf,spdlog,argparse,LLVM,libbpf} > .deps/copyright
}

if [[ $(cat .deps/runtime/BUILD 2>/dev/null) == "$identity" ]] && verify_runtime 2>/dev/null && assemble_notices; then
    chmod 755 .deps/runtime/{bpftime,libbpftime-agent.so,libbpftime-syscall-server.so}
    exit 0
fi
rm -f .deps/runtime/BUILD
mkdir -p .deps/runtime .deps/notices/runtime
if [[ $(uname -m) == aarch64 ]]; then
    curl --fail --location --retry 3 "$archive" -o .deps/bpftime-arm64.tar.gz
    printf '%s  %s\n' "$archive_hash" .deps/bpftime-arm64.tar.gz | sha256sum --check --status
    tar -xzf .deps/bpftime-arm64.tar.gz -C .deps
else
    docker pull --platform linux/amd64 "$image"
    container=$(docker create --platform linux/amd64 "$image")
    trap 'docker rm "$container" >/dev/null' EXIT
    for binary in bpftime libbpftime-agent.so libbpftime-syscall-server.so; do
        docker cp "$container:/root/.bpftime/$binary" ".deps/runtime/$binary"
    done

    for notice in LICENSE:bpftime third_party/spdlog/LICENSE:spdlog third_party/argparse/LICENSE:argparse vm/llvm-jit/LICENSE:llvmbpf; do
        docker cp "$container:/bpftime/${notice%:*}" ".deps/notices/runtime/${notice#*:}"
    done
    docker cp "$container:/usr/share/doc/llvm-16/copyright" .deps/notices/runtime/LLVM
    docker cp "$container:/bpftime/third_party/bpftool/libbpf/LICENSE.BSD-2-Clause" .deps/notices/runtime/libbpf
fi
verify_runtime
assemble_notices
printf '%s\n' "$identity" > .deps/runtime/BUILD
