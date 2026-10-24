# Toolchain image the native CI jobs run in.
#
# It carries the system packages, Go and Rust those builds need, at the
# versions the workflows pin. Jobs reference it by digest, so a rebuild
# changes nothing until a follow-up change pins the new digest.

FROM ubuntu:noble@sha256:68ee22004a9109b44973313b02ff7b1de250f1de0f5ecb628d73f579aaff33ac AS ci

LABEL org.opencontainers.image.source=https://github.com/yanet-platform/yanet2

ARG TARGETARCH
ARG GO_VERSION=1.27.3
ARG GO_SHA256_AMD64=65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40
ARG GO_SHA256_ARM64=a06e99309dbcbdca1a8ccd2282c3ee146e55930886e26b4f1193269587165f49
ARG RUSTUP_VERSION=1.29.2
ARG RUSTUP_SHA256_AMD64=5be191e8c70a5007074254cb74d78dbfdd25cd5a61c27de7b8e5410122ec24f4
ARG RUSTUP_SHA256_ARM64=a9c168dbea81a84671c6b7a5c1795cb0eb699020110d107ddfca427bd797e697
ARG RUST_VERSION=1.99.1
ARG PROTOC_GEN_GO_VERSION=v1.36.11
ARG PROTOC_GEN_GO_GRPC_VERSION=v1.6.1

# A newer Go requested by a module fails the build instead of being fetched,
# so the jobs run only the Go this image carries.
ENV DEBIAN_FRONTEND=noninteractive \
    CARGO_HOME=/usr/local/cargo \
    RUSTUP_HOME=/usr/local/rustup \
    GOTOOLCHAIN=local \
    PATH=/usr/local/cargo/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# Recommended packages stay out, so everything the builds use is listed.
#
# The protobuf headers carry the well-known types the protos import, the
# compiler runtime carries libFuzzer and clang's sanitizers, and without zstd
# workflow caches fall back to gzip and no longer match the entries that jobs
# on the hosted runner save.
RUN set -x \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
        bison \
        build-essential \
        ca-certificates \
        ccache \
        clang \
        cmake \
        curl \
        flex \
        git \
        jq \
        libclang-rt-18-dev \
        libnuma-dev \
        libpcap-dev \
        libprotobuf-dev \
        libyaml-dev \
        meson \
        ninja-build \
        pkg-config \
        protobuf-compiler \
        python3-pyelftools \
        xz-utils \
        zstd \
 && rm -rf /var/lib/apt/lists/*

# Jobs build as root in a workspace the runner user owns.
RUN git config --system --add safe.directory '*'

# Go and the Rust installer are checked against the digests pinned above; the
# installer verifies the toolchain it fetches against the release manifest.
RUN set -eux; \
    case "$TARGETARCH" in \
        amd64) go_sha256=$GO_SHA256_AMD64 rustup_sha256=$RUSTUP_SHA256_AMD64 rust_target=x86_64-unknown-linux-gnu ;; \
        arm64) go_sha256=$GO_SHA256_ARM64 rustup_sha256=$RUSTUP_SHA256_ARM64 rust_target=aarch64-unknown-linux-gnu ;; \
        *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/go.tar.gz "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz"; \
    echo "$go_sha256  /tmp/go.tar.gz" | sha256sum -c -; \
    tar -C /usr/local -xzf /tmp/go.tar.gz; \
    rm /tmp/go.tar.gz; \
    curl -fsSL -o /tmp/rustup-init "https://static.rust-lang.org/rustup/archive/${RUSTUP_VERSION}/${rust_target}/rustup-init"; \
    echo "$rustup_sha256  /tmp/rustup-init" | sha256sum -c -; \
    chmod +x /tmp/rustup-init; \
    /tmp/rustup-init -y --no-modify-path --profile minimal --default-toolchain "$RUST_VERSION"; \
    rm /tmp/rustup-init

# The protobuf code generators the build runs, at the versions the jobs on
# the hosted runner install.
RUN set -eux; \
    export GOBIN=/usr/local/bin GOPATH=/tmp/gopath GOCACHE=/tmp/gocache; \
    go install "google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION}"; \
    go install "google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION}"; \
    rm -rf /tmp/gopath /tmp/gocache
