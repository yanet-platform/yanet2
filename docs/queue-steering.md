# Queue steering

Each worker entry of a device can declare which traffic its port queue
takes: whether the queue is in the port's RSS table, and which traffic
classes the NIC steers to it through `rte_flow` rules. The `rss` and
`steer` keys also apply to an entry of a device's `queues:` list, which
declares the queue a worker assignment later binds (see
[queues and workers](queues-and-workers.md)). The `rss` field
alone sets a queue's role:

- **RSS queue** (`rss: true`, the default): receives hashed traffic, plus
  any classes it steers;
- **steering queue** (`rss: false`): receives only the classes it steers.

A worker polling a steering queue is a steering worker; any other worker is
a data worker. Taps (`virtio_user_` ports) and `net_ring_` ports have no role.

The main use is control-plane isolation. ARP, IPv6 neighbour discovery,
LACP/LLDP/STP, and BGP or management gRPC marked with a listed DSCP value go
to a dedicated worker, so they keep reaching the host when the data workers
are saturated. Only ingress is isolated: replies from the host still share
the data path. The examples below show that setup.

## Configuration

```yaml
devices:
  - port_name: "0000:01:00.0"
    rss_hash: <rss_hf>
    workers:
      - core_id: 1                 # default: in the RSS table
      - core_id: 2
      - core_id: 5
        rss: false                 # left out of the RSS table
        steer:
          dscp: [48]               # values 1..63, IPv4 and IPv6, ECN ignored
          arp: true
          nd: true                 # ICMPv6 types 133-136
          l2_control: true         # 01:80:c2:00:00:0X: STP, LACP, LLDP
```

- **`rss`** (default `true`): whether the queue is in the port's RSS table.
- **`steer`**: classes the NIC sends to the queue through `rte_flow` rules.
  When several queues list a class, an `rte_flow` RSS action spreads it over
  them by IP hash; non-IP classes (`arp`, `l2_control`) then land on one of
  them. `steer` does not change a queue's role: on an RSS queue the classes
  still mix with hashed traffic, so isolation needs a steering queue.

Startup rejects:

- steering on a `virtio_user_` or `net_ring_` port;
- `dscp: 0`, which is all unmarked traffic;
- a queue with `rss: false` and no class, which would receive nothing;
- queue 0 out of the RSS table: without RSS all unmatched traffic lands on
  queue 0;
- ND and a listed DSCP value on different queues, since one packet can match
  both.

## How traffic flows

- **Ingress.** After the port starts, the RSS table is rewritten to the RSS
  queues only, then the steering rules are installed. Unmatched traffic never
  reaches a steering queue. Without RSS (`rss_hash: 0`) the table is left
  alone and unmatched traffic lands on queue 0.
- **Modules.** `dp_config_device_rss_queue_count` reports how many of a
  port's queues are RSS queues; the device's worker count counts the
  workers bound to it, once per worker however many of its queues —
  steering queues included — that worker polls.
- **Pipes.** A steering worker pipes into other ports' steering queues, or
  their RSS queues when they have none. A data worker pipes into RSS queues
  only, so data never lands on a steering queue.
  Devices without steering, such as the tap, are wired as before.
- **Host egress is not isolated.** The tap's worker is a data worker, so
  traffic the host sends leaves through data workers' pipes and transmit
  queues and can wait behind data.
- **Pipeline.** All queues of a port stay one device and run the same
  pipeline.

## Failure behaviour

| Situation | Effect |
|---|---|
| A rule the driver rejects | Startup aborts; the rules already installed are flushed |
| Driver without flow API (virtio) | Startup aborts |
| RSS table rewrite refused | Startup aborts |

## Requirements

- **Remark DSCP at the border.** The sender sets DSCP, so without remarking
  anyone can mark a flood CS6 and load the steering queue. List only values
  reserved for control-plane traffic (CS6/CS7).
- **Filter host-bound traffic.** Route the router's own addresses through an
  ACL before the tap, for example `forward` into a logical `host` device
  running `acl → forward OUT kni0`, so floods are dropped on the data
  workers.
- **Steered flows leave RSS affinity.** Matched traffic is processed by the
  steering worker, not the one RSS picks. Keep steered classes away from
  stateful processing that depends on RSS worker affinity.

Steering protects ingress against the software bottleneck: busy workers and
full receive queues. Drops in the NIC's shared receive buffer happen before
steering and need NIC QoS (PFC or ETS on the switch and NIC).

## Hardware

Steering is vendor-neutral. A port needs a DPDK driver that supports:

- updating the RSS table (`rte_eth_dev_rss_reta_update`), when RSS is on
  and some queue has `rss: false`;
- `rte_flow` rules with the patterns of each listed class, and a `QUEUE`
  action, or an `RSS` action for a class several queues list.

Classes are opt-in per queue, so a port uses only the matches its NIC
supports. Startup fails with the port and class named when the driver
rejects a rule, so an unsupported match never silently degrades. Each NIC
model in use (for example mlx5, ice, i40e) needs a hardware check before
deployment. On mlx5 keep the default flow engine, `dv_flow_en=1`.

The QEMU functional harness can't exercise steering: its NICs are
single-queue and virtio has no flow API.
