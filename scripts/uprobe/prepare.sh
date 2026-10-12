#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
releases=https://github.com/yanet-platform/yanet2/releases/download
case $(uname -m) in
x86_64)
    arch=amd64
    identity=v0.9.0-amd64@sha256:a72c5eb1b87161daf0ecc8ff1518618759de014f581c81f6cb05bf780c488c7a
    archive=$releases/bpftime-v0.9.0-amd64-38245741705-1/bpftime-amd64.tar.gz
    archive_hash=0b301306bb55f85511bff0b6daaf7778ce629dd4173a4f9b3280ff7750e40f6f
    hashes='72f3a0d27eacf97eb63b264fa20236cf9866c2df3f6fb09f721e81aec2a319c1  bpftime
366e46b2c76be80493a80688d534e25926a8120fd1b6aa13823247bab1f99e28  libbpftime-agent.so
cbe6b21b6a94f9a3fcc681100d8e3f7c3416cf86b6eb532b1d23b535c75df3ec  libbpftime-syscall-server.so'
    ;;
aarch64)
    arch=arm64
    identity=v0.9.0-arm64@sha256:f5d0287b32dd8c933036e553eab36a8d067a152a7d99536123b6005c3f8c960f
    archive=$releases/bpftime-v0.9.0-arm64-37632565870-1/bpftime-arm64.tar.gz
    archive_hash=c2d7202f814bc735928f5bbf17f155ceb8db01b82ce0e7e4b40fc94e3ed9b77c
    hashes='c5ee2e55700417889acdce881b7fce842a73fea983686ee955786ab2a412f18d  bpftime
b6bbff93881f7e2093bae25df27b64ae714948914a8c136a4891591a7b019529  libbpftime-agent.so
2adb1636a660644d2dd5f3b6953c2c5ab182c170846bea786bf7789067f0e0ad  libbpftime-syscall-server.so'
    ;;
*) echo 'yanet-uprobe supports amd64 and arm64 only' >&2; exit 1 ;;
esac
download=.deps/bpftime-$arch.tar.gz

verify_runtime() (
    cd .deps/runtime
    sha256sum --check --status <<< "$hashes"
)

verify_archive() {
    printf '%s  %s\n' "$archive_hash" "$download" | sha256sum --check --status
}

assemble_notices() {
    for notice in .deps/notices/runtime/{bpftime,llvmbpf,spdlog,argparse,LLVM,libbpf}; do
        [[ -s $notice ]] || return 1
    done
    cat ../../LICENSE .deps/notices/runtime/{bpftime,llvmbpf,spdlog,argparse,LLVM,libbpf} > .deps/copyright
}

if [[ $(cat .deps/runtime/BUILD 2>/dev/null) == "$identity" ]] && verify_runtime 2>/dev/null && assemble_notices; then
    chmod 755 .deps/runtime/{bpftime,libbpftime-agent.so,libbpftime-syscall-server.so}
    exit 0
fi
rm -rf .deps/runtime .deps/notices/runtime
mkdir -p .deps
# A pre-placed archive with the pinned hash allows offline builds.
if ! verify_archive 2>/dev/null; then
    curl --fail --location --retry 3 "$archive" -o "$download"
    verify_archive
fi
tar -xzf "$download" -C .deps runtime notices/runtime
verify_runtime
assemble_notices
[[ $(cat .deps/runtime/BUILD) == "$identity" ]]
