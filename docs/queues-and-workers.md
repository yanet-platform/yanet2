# Queues and workers

A device's queues are its rx/tx pairs: each queue has its own receive
ring, transmit ring and receive pool. A worker thread serves queues — one
assignment per (device, queue) pair — and a queue is assigned to exactly
one worker. One worker may hold assignments of several devices, so a
lightly loaded system can serve a port queue and the host tap from one
core instead of one core per queue.

The queue/worker layout is declared in the dataplane YAML: devices
declare their queues, a top-level `workers:` section binds cores to
assignments of device and queue pairs.

## Declaring queues

A device declares its queues through exactly one of three shapes:

```yaml
devices:
  - port_name: 01:00.0
    rss_hash: <rss_hf>
    queue_count: 4              # 4 queues at the device defaults

  - port_name: virtio_user_kni0
    mac_addr: 52:54:00:6b:ff:a5
    default_queue:
      rx_queue_len: 1024        # device-wide defaults for unset fields
      tx_queue_len: 1024
    queues:
      - num_mbufs: 4096         # a per-queue override
```

- **`queue_count`**: that many queues, each at the device defaults.
- **`queues`**: a list declaring each queue on its own; an empty list is
  rejected.
- **`workers` (nested)**: the legacy shape. Entry *i* of the device's
  `workers:` list declares queue *i* — its sizing, and its steering role
  (`rss`/`steer`, see [queue steering](queue-steering.md)) — and polls it
  from one worker of its own. It cannot be mixed with a top-level
  `workers:` section.

A queue's id is its position in the declaration, which is the index the
port's RSS table spreads traffic over.

Queue fields: `rx_queue_len`, `tx_queue_len` (ring descriptors) and
`num_mbufs` (receive pool size). A field a queue leaves unset takes the
device's `default_queue` value, and one still unset takes the built-in
default: 4096 descriptors per ring, 16384 mbufs. The role keys `rss` and
`steer` also live on queue declarations; a `default_queue` block takes
sizing keys only.

## Binding workers

```yaml
workers:
  - core_id: 1
    instance_id: 0
    assignments:
      - device: 01:00.0         # matches device_name, which defaults to
        queue: 0                # port_name; connections use the same name
        weight: 2               # rx bursts per round, 1..16, default 1
      - device: virtio_user_kni0
        queue: 0
```

Every declared queue is assigned to exactly one worker, and every worker
holds at least one assignment. Assignments of one device may sit on
different workers, and one worker may mix assignments of several devices.
Within one worker, the first assignment listed for a device is that
worker's designated transmit queue for the device's locally produced
traffic; later assignments of the same device receive, but do not carry
the worker's own transmissions.

**`weight` is the assignment's rx service.** Every worker round serves
each of its assignments with up to `weight` receive bursts of at most 32
packets; a burst that comes back shorter than requested leaves the queue
drained and ends that assignment's service for the round. Weight 1 — the
default, and all the nested form has ever served — is one burst per
round. Higher weights drain a busy queue faster at the cost of the
round's other assignments.

## Startup rejects

- a device declaring queues through more than one of `queue_count`,
  `queues` and `workers`;
- a top-level `workers:` section beside nested `devices[].workers[]`
  entries;
- a queue no worker polls, a queue two workers poll, or a worker without
  assignments;
- an assignment naming an unknown device, a device declared more than
  once, or a queue outside the device's declared range;
- a `net_ring_` device whose declared queue count is not exactly one;
- a worker mixing a steering queue with receive queues: the pipe wiring
  classifies a worker as a whole, so a steering queue needs a dedicated
  worker (see [queue steering](queue-steering.md) for the role rules).

## Observing

`yanet-cli-counters workers` lists every worker with its assignments —
device, queue and weight — the rx/tx counters, and the `rx_bursts`
histogram: one slot per burst size, slot 0 counting empty polls. The
counters and the histogram are worker-wide: every assignment of a worker
counts into the same slots, so they show how full the worker's polls run
as a whole and cannot attribute a full or short burst to one assignment.
An unused part of a weight costs nothing: a short burst ends the
assignment's service for the round.
