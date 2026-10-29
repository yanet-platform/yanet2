# Host preparation and dataplane.yaml

Everything a box needs before the control plane can attach, plus the full
dataplane YAML grammar. Annotated shipped sample: repo root `dataplane.yaml`
(installed as `/etc/yanet2/dataplane-default.yaml`); grammar source:
`dataplane/config.c`.

## Host preparation

### Hugepages

The shared-memory file and DPDK mbuf pools live on hugepages. Quick setup:

```bash
sudo ./subprojects/dpdk/usertools/dpdk-hugepages.py --setup 8G
```

Persistent (production) setup - sysctl plus the hugetlbfs mount:

```bash
echo 'vm.nr_hugepages = 4096' | sudo tee /etc/sysctl.d/90-yanet-hugepages.conf
sudo sysctl --system
sudo mkdir -p /dev/hugepages
sudo mount -t hugetlbfs nodev /dev/hugepages
echo 'nodev /dev/hugepages hugetlbfs defaults 0 0' | sudo tee -a /etc/fstab
```

Sizing: hugepages must cover `storage` (= sum of all instances'
`dp_memory + cp_memory`) + `dpdk_memory` + per-worker mbuf pools, with head
room. A full-default deployment (acl + fwstate at 8 GB each) needs ~24 GiB of
hugepages before any traffic flows.

`ENOMEM` when opening the storage file almost always means "not enough
preallocated pages" - check `grep Huge /proc/meminfo` before debugging
anything else.

### NIC binding

```bash
sudo modprobe vfio-pci            # or: sudo modprobe uio_pci_generic
sudo ./subprojects/dpdk/usertools/dpdk-devbind.py --status
sudo ./subprojects/dpdk/usertools/dpdk-devbind.py --bind=vfio-pci 01:00.0
```

Mellanox ConnectX-4/5/6 are the production-tested family (Yandex maintainer
statement, yanet-platform/yanet#39). In VMs, virtio works and needs
`iova_mode: pa` under vfio no-iommu/uio.

### CPU planning

- One worker per RX queue, each pinned to its own `core_id`; DPDK workers
  busy-poll and will show 100 % CPU - that is normal.
- Leave cores for the OS, gateway, operators and BIRD.
- Yandex production practice: hyperthreading and turbo boost disabled in
  BIOS, cores strictly pinned (habr 839508, see field-notes).

## dataplane.yaml grammar

```yaml
dataplane:
  storage: /dev/hugepages/yanet     # shm file; sized to sum(dp+cp) at start
  dpdk_memory: 1024                 # MiB passed to DPDK EAL (-m)
  packet_recirc_limit: 64           # 4..256 self-redirects per packet lineage
  # iova_mode: pa                   # pa | va; pa for uio / vfio no-iommu
  # loglevel: info
  # plugin_dir: /usr/lib/yanet2/modules   # optional .so module plugins
  # modules: [acl, route]                  # restrict loaded module set
  instances:
    - dp_memory: 1 GiB              # bytes with KiB/MiB/GiB/TiB units
      cp_memory: 2 GiB              # must cover sum of module memory_requirements
      numa_id: 0                    # one instance per NUMA node on big boxes
  devices:
    - port_name: 01:00.0            # PCI addr | virtio_user_<iface> | net_ring_...
      # device_name: uplink0        # optional; defaults to port_name
      mac_addr: 52:54:00:6b:ff:a5   # needed for virtio_user ports
      mtu: 7000                     # jumbo frames for DC fabrics
      max_lro_packet_size: 7200     # >= mtu
      rss_hash: 0
      workers:
        - core_id: 1                # one entry per RX/TX queue
          instance_id: 0
          rx_queue_len: 1024
          tx_queue_len: 1024
          # num_mbufs: 2048
  connections:                      # worker-to-worker pipes between devices;
    - src_device: 01:00.0           # required by cross-device module actions
      dst_device: virtio_user_kni0  # (forward/mirror rules targeting another
    - src_device: virtio_user_kni0  # device); wire both directions
      dst_device: 01:00.0
```

### Port types (`port_name`)

| Value | Meaning |
|---|---|
| `01:00.0` (PCI address) | Physical NIC bound to vfio-pci/uio. YANET owns the link. |
| `virtio_user_kni0` | vdev over `/dev/vhost-net` attached to kernel iface `kni0` - the kernel-facing side (management, BGP). Requires `mac_addr`. |
| `net_ring_:<socket_id>:<ringname>` | SP/SC ring port for tests, exactly one worker. |

### How `connections` interact with pipelines

Every received packet is scheduled into the **input pipelines assigned to its
device** (via the pipeline operator or device CLI). After module processing,
packets whose `tx_device_id` equals the current worker's device go straight
out that port; packets targeting another device travel the `connections`
pipes to the destination device's worker. So `connections` do not bypass
modules - they are the cross-device transport module actions rely on. Before
any config is installed at all, the dataplane drops traffic.

### Memory math checklist

1. Decide the module set for the role (see SKILL.md table).
2. Sum every module's + device adapter's `memory_requirements` from the
   controlplane YAML; add ~10 % head room -> `cp_memory`.
3. `dp_memory` covers the dataplane's own structures; 1 GiB is the shipped
   starting point.
4. `storage` file = sum over instances of both, allocated from hugepages at
   start - hugepages must cover it plus `dpdk_memory`.
