#!/bin/bash
# Restarts the control plane with the build that uses the Rust decap api,
# then reapplies the lab's common configuration to the new process.
set -euo pipefail

cli=/tmp/yanet/cli
binary=/tmp/yanet/build/controlplane/yanet-controlplane-rustcp

mount -a 2>/dev/null || true
cp /mnt/build/controlplane/yanet-controlplane-rustcp "$binary"
chmod +x "$binary"

pkill -f '^/tmp/yanet/build/controlplane/yanet-controlplane -c' || true
for _ in $(seq 100); do
    pgrep -f '^/tmp/yanet/build/controlplane/yanet-controlplane -c' >/dev/null || break
    sleep 0.1
done
nohup "$binary" -c /tmp/yanet/config/controlplane.yaml \
    >/tmp/yanet/logs/yanet-controlplane.log 2>&1 </dev/null &
for _ in $(seq 100); do
    "$cli/yanet-cli-decap" list >/dev/null 2>&1 && break
    sleep 0.2
done

"$cli/yanet-cli-forward" update --name=forward0 /tmp/yanet/forward.yaml
"$cli/yanet-cli-route" fib update --name=route0 /tmp/yanet/config/route0.yaml
"$cli/yanet-cli-function" update --name=virt --chains chain0:10=forward:forward0
"$cli/yanet-cli-function" update --name=test --chains chain2:1=forward:forward0,route:route0
"$cli/yanet-cli-pipeline" update --name=bootstrap --functions virt
"$cli/yanet-cli-pipeline" update --name=dummy --functions
"$cli/yanet-cli-device-plain" update --name=01:00.0 --input test:1 --output dummy:1
"$cli/yanet-cli-device-plain" update --name=virtio_user_kni0 --input bootstrap:1 --output dummy:1
echo "control plane: $(readlink "/proc/$(pgrep -f '^/tmp/yanet/build/controlplane/yanet-controlplane-rustcp')/exe")"
