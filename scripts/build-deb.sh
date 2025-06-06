#!/bin/bash
set -euxo pipefail
env

# Install build deps with verbose output
apt-get update
(
    curdir=$(pwd)
    mkdir -p /tmp/build
    cd /tmp/build 
    echo y | mk-build-deps --install -t 'apt-get -o Debug::pkgProblemResolver=yes --no-install-recommends -y --allow-downgrades --allow-remove-essential --allow-change-held-packages' $curdir/debian/control
) 2>&1 | tee build-deps.log

echo "Current PATH during build: $PATH" > build-path.log
# Preserve environment variables for cargo (order matters in dpkg-buildpackage)
echo y | debuild --preserve-envvar=PATH -us -uc 2>&1 | tee build.log 

mkdir -p outdeb
dcmd cp ../*.changes outdeb/
