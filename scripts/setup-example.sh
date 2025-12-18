#!/bin/bash

set -e

yanet-cli-forward update --cfg forward0 --rules /etc/yanet2/forward.yaml

yanet-cli-pipeline update --name=dummy

yanet-cli-function update --name=virt --chains chain0:10=forward:forward0
yanet-cli-pipeline update --name=virt --functions virt
yanet-cli-device-plain update --name=virtio_user_kni0 --input virt:1 --output dummy:1

sleep 3

yanet-cli-route insert --cfg route0 --via 2a02:6b8:0:320::1ab:a1a ::/0
yanet-cli-route insert --cfg route0 --via 5.255.198.70 0.0.0.0/0
yanet-cli-balancer update --name balancer0 --config /etc/yanet2/balancer.yaml
yanet-cli-function update --name=phy --chains chain0:10=forward:forward0,balancer:balancer0,route:route0
yanet-cli-pipeline update --name=phy --functions phy
yanet-cli-device-plain update --name=5e:00.0 --input phy:1 --output dummy:1

sleep 3

yanet-cli-route insert --cfg route0 --via 2a02:6b8:0:320::1ab:a1a ::/0
yanet-cli-route insert --cfg route0 --via 5.255.198.70 0.0.0.0/0