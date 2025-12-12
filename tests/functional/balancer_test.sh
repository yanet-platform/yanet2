# Bind devices to DPDK
/mnt/yanet2/subprojects/dpdk/usertools/dpdk-devbind.py --bind=vfio-pci 01:00.0 || echo "Device binding failed or already bound"

# Start dataplane with logging
/mnt/build/dataplane/yanet-dataplane /mnt/build/dataplane.yaml > /mnt/build/yanet-dataplane.log 2>&1 &
DATAPLANE_PID=$!
echo "Started dataplane with PID: $DATAPLANE_PID"
sleep 1

# Start controlplane with logging
/mnt/build/controlplane/yanet-controlplane -c /mnt/build/controlplane.yaml > /mnt/build/yanet-controlplane.log 2>&1 &
CONTROLPLANE_PID=$!
echo "Started controlplane with PID: $CONTROLPLANE_PID"
sleep 3

ip link set kni0 up
ip nei add fe80::1 lladdr 52:54:00:6b:ff:a1 dev kni0
ip nei add 203.0.113.1 lladdr 52:54:00:6b:ff:a1 dev kni0
sleep 3

/mnt/target/release/yanet-cli-route insert --cfg route0 --instances 0 --via fe80::1 ::/0
/mnt/target/release/yanet-cli-route insert --cfg route0 --instances 0 --via 203.0.113.1 0.0.0.0/0

/mnt/target/release/yanet-cli-balancer update --name balancer0 --config /mnt/yanet2/balancer.yaml

/mnt/target/release/yanet-cli-balancer config --name balancer0

/mnt/target/release/yanet-cli-function update --name=test --chains ch0:2=balancer:balancer0,route:route0 --instance=0

/mnt/target/release/yanet-cli-pipeline update --name=test --functions test --instance=0

/mnt/target/release/yanet-cli-pipeline update --name=dummy --instance=0

/mnt/target/release/yanet-cli-device-plain update --name=01:00.0 --input test:1 --output dummy:1 --instance=0

/mnt/target/release/yanet-cli-balancer stats --name=balancer0 --device=01:00.0 --pipeline=test --function=test --chain=ch0