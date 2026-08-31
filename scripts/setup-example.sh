#!/bin/bash

set -e

script_path=${BASH_SOURCE[0]}
script_dir=${script_path%/*}
if [[ "$script_dir" == "$script_path" ]]; then
    script_dir=.
fi
cd -P -- "$script_dir"
script_dir=$PWD
repo_root=${script_dir%/scripts}

yanet-cli-forward update --name=forward0 "$repo_root/forward.yaml"

yanet-cli-pipeline update --name=dummy --functions

yanet-cli-function update --name=virt --chains chain0:10=forward:forward0
yanet-cli-pipeline update --name=virt --functions virt
yanet-cli-device-plain update --name=virtio_user_kni0 --input virt:1 --output dummy:1

yanet-cli-function update --name=phy --chains chain0:10=forward:forward0
yanet-cli-pipeline update --name=phy --functions phy
yanet-cli-device-plain update --name=01:00.0 --input phy:1 --output dummy:1
