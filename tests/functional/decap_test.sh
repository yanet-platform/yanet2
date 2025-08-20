#!/bin/bash

set -ex

# Cleanup function - only called on error
cleanup() {
    echo "Error occurred, cleaning up processes..."
    if [ ! -z "$CONTROLPLANE_PID" ]; then
        kill $CONTROLPLANE_PID 2>/dev/null || true
        echo "Stopped controlplane (PID: $CONTROLPLANE_PID)"
    fi
    if [ ! -z "$DATAPLANE_PID" ]; then
        kill $DATAPLANE_PID 2>/dev/null || true
        echo "Stopped dataplane (PID: $DATAPLANE_PID)"
    fi
}

trap cleanup ERR

# Bind devices to DPDK
/mnt/yanet2/subprojects/dpdk/usertools/dpdk-devbind.py --bind=vfio-pci 01:00.0 || echo "Device binding failed or already bound"

# Start dataplane with logging
/mnt/build/dataplane/yanet-dataplane /mnt/build/dataplane.yaml > /mnt/build/yanet-dataplane.log 2>&1 &
DATAPLANE_PID=$!
echo "Started dataplane with PID: $DATAPLANE_PID"
sleep 5

# Start controlplane with logging
/mnt/build/controlplane/yanet-controlplane -c /mnt/build/controlplane.yaml > /mnt/build/yanet-controlplane.log 2>&1 &
CONTROLPLANE_PID=$!
echo "Started controlplane with PID: $CONTROLPLANE_PID"
sleep 5

# Setup network interfaces and neighbors
ip link set kni0 up
ip nei add fe80::1 lladdr 52:54:00:6b:ff:a1 dev kni0
ip nei add 203.0.113.1 lladdr 52:54:00:6b:ff:a1 dev kni0
sleep 3

# Enable L2 forwarding between devices
/mnt/target/release/yanet-cli-forward l2-enable --cfg=forward0 --instances 0 --src 0 --dst 1
/mnt/target/release/yanet-cli-forward l2-enable --cfg=forward0 --instances 0 --src 1 --dst 0

# Add L3 forwarding rules
/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 1 --dst 0 --net 0.0.0.0/0
/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 1 --dst 0 --net ::/0
/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 0 --dst 1 --net 203.0.113.14/32
/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 0 --dst 1 --net fe80::5054:ff:fe6b:ffa5/64
/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 0 --dst 1 --net ff02::/16

# Route
/mnt/target/release/yanet-cli-route insert --cfg route0 --instances 0 --via fe80::1 ::/0
/mnt/target/release/yanet-cli-route insert --cfg route0 --instances 0 --via 203.0.113.1 0.0.0.0/0

# Add decap prefixes (outer tunnel destination addresses)
# IPv4 prefix for IPIP6 (IPv6-in-IPv4) tunnels
/mnt/target/release/yanet-cli-decap prefix-add --cfg decap0 --instances 0 -p 4.5.6.7/32
# IPv6 prefix for IP6IP (IPv4-in-IPv6) tunnels
/mnt/target/release/yanet-cli-decap prefix-add --cfg decap0 --instances 0 -p 1:2:3:4::abcd/128

# Configure pipelines (following docs/virtual-run.org)
/mnt/target/release/yanet-cli-pipeline update --name=bootstrap --modules forward:forward0 --instance=0
/mnt/target/release/yanet-cli-pipeline update --name=decap --modules forward:forward0 --modules decap:decap0 --modules route:route0 --instance=0

# Assign pipelines to devices (use device names, not IDs)
#/mnt/target/release/yanet-cli-pipeline assign --instance=0 --device=00:03.0 --pipelines decap:1
/mnt/target/release/yanet-cli-pipeline assign --instance=0 --device=01:00.0 --pipelines decap:1
/mnt/target/release/yanet-cli-pipeline assign --instance=0 --device=virtio_user_kni0 --pipelines bootstrap:1

# Inspect configuration
/mnt/target/release/yanet-cli-inspect
/mnt/target/release/yanet-cli-route show --cfg route0 --instances 0

