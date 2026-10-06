#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
GOTOOLCHAIN=${GOTOOLCHAIN:-go1.27.1} go version
[[ $(uname -m) == x86_64 ]] || { echo 'the prebuilt cp-lock runtime is available for amd64 only' >&2; exit 1; }
image=ghcr.io/eunomia-bpf/bpftime@sha256:6e315ea561d08e482159385a4775ee6564fc4a6f416f24ae40bfccb176191ed9
identity=v0.9.0@sha256:6e315ea561d08e482159385a4775ee6564fc4a6f416f24ae40bfccb176191ed9
rm -f .deps/runtime/BUILD
docker pull --platform linux/amd64 "$image"
container=$(docker create --platform linux/amd64 "$image")
trap 'docker rm "$container" >/dev/null' EXIT
mkdir -p .deps/runtime .deps/notices/runtime
for binary in bpftime libbpftime-agent.so libbpftime-syscall-server.so; do
    docker cp "$container:/root/.bpftime/$binary" ".deps/runtime/$binary"
done
(cd .deps/runtime && sha256sum --check --status <<'HASHES'
d7ce69e5f1b1fc6d55483964a54c5d98588df4978b3dd0042208e4c84dadd2b5  bpftime
324ec32e22942c41d7d5725d5c27907d8ba60f29b2bfbf62d9725fcbc6cff027  libbpftime-agent.so
025a480229372eebf8b552aa2d718d9b72b4d20676f61ba0073801bb42be6955  libbpftime-syscall-server.so
HASHES
)

for notice in LICENSE:bpftime third_party/spdlog/LICENSE:spdlog third_party/argparse/LICENSE:argparse vm/llvm-jit/LICENSE:llvmbpf; do
    docker cp "$container:/bpftime/${notice%:*}" ".deps/notices/runtime/${notice#*:}"
done
docker cp "$container:/usr/share/doc/llvm-16/copyright" .deps/notices/runtime/LLVM
docker cp "$container:/bpftime/third_party/bpftool/libbpf/LICENSE.BSD-2-Clause" .deps/notices/runtime/libbpf
cat ../../../LICENSE .deps/notices/runtime/{bpftime,llvmbpf,spdlog,argparse,LLVM,libbpf} > .deps/copyright
printf '%s\n' "$identity" > .deps/runtime/BUILD
