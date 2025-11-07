/mnt/yanet2/subprojects/dpdk/usertools/dpdk-devbind.py --bind=vfio-pci 01:00.0 || echo "Device binding failed or already bound"

/mnt/build/dataplane/yanet-dataplane /mnt/build/dataplane.yaml > /mnt/build/yanet-dataplane.log 2>&1 &
DATAPLANE_PID=$!
echo "Started dataplane with PID: $DATAPLANE_PID"
sleep 2

/mnt/build/controlplane/yanet-controlplane -c /mnt/build/controlplane.yaml > /mnt/build/yanet-controlplane.log 2>&1 &
CONTROLPLANE_PID=$!
echo "Started controlplane with PID: $CONTROLPLANE_PID"
sleep 2

ip link set kni0 up
ip nei add fe80::1 lladdr 52:54:00:6b:ff:a1 dev kni0
ip nei add 203.0.113.1 lladdr 52:54:00:6b:ff:a1 dev kni0
sleep 2
