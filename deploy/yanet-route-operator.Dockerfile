# syntax=docker/dockerfile:1

FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN echo 'APT::Sandbox::User "root";' > /etc/apt/apt.conf.d/99sandbox

COPY deploy/packages/yanet2-route-operator_*.deb /tmp/

RUN apt-get update -y \
    && apt-get install -y --no-install-recommends /tmp/*.deb \
    && rm -rf /tmp/*.deb /var/lib/apt/lists/*

ENTRYPOINT ["yanet-route-operator", "-c", "/etc/yanet2/yanet-route-operator.yaml"]
