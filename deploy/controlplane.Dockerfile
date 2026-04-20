# syntax=docker/dockerfile:1

FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN echo 'APT::Sandbox::User "root";' > /etc/apt/apt.conf.d/99sandbox

# yanet2-controlplane depends on yanet2-dataplane, so both debs must be present.
COPY deploy/packages/yanet2-dataplane_*.deb deploy/packages/yanet2-controlplane_*.deb /tmp/

RUN apt-get update -y \
    && apt-get install -y --no-install-recommends /tmp/*.deb \
    && rm -rf /tmp/*.deb /var/lib/apt/lists/*

ENTRYPOINT ["yanet-controlplane", "-c", "/etc/yanet2/controlplane.yaml"]
