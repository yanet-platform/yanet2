# syntax=docker/dockerfile:1

# Build stage: compile yanet-dataplane from source.
FROM ubuntu:24.04 AS build

ENV DEBIAN_FRONTEND=noninteractive

# Allow apt to work in rootless container builds (no user namespace).
RUN echo 'APT::Sandbox::User "root";' > /etc/apt/apt.conf.d/99sandbox

RUN apt-get update -y && apt-get install -y --no-install-recommends \
    clang \
    libibverbs-dev \
    libnuma-dev \
    libpcap-dev \
    libyaml-dev \
    meson \
    pkg-config \
    python3-pyelftools \
    rdma-core \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY . .

RUN CC=clang meson setup build \
    -Dbuildtype=release \
    -Doptimization=2 \
    -Dprefix=/usr \
    -Ddataplane_only=true

RUN meson compile -C build dataplane/yanet-dataplane \
    && strip --strip-unneeded build/dataplane/yanet-dataplane

# Runtime stage: minimal image with only what the dataplane needs.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN echo 'APT::Sandbox::User "root";' > /etc/apt/apt.conf.d/99sandbox && \
    apt-get update -y && apt-get install -y --no-install-recommends \
    ibverbs-providers \
    libibverbs1 \
    libnuma1 \
    libyaml-0-2 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /src/build/dataplane/yanet-dataplane /usr/bin/yanet-dataplane

ENTRYPOINT ["yanet-dataplane"]
