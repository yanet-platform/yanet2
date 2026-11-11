# Metrics reference

Operator-facing metrics exported by the built-in counters service and
collected through the gateway metrics endpoints. All worker and port
families come from the dataplane shared-memory counter storage; a family
whose collection fails is omitted from the snapshot with a logged
warning — values are never synthesized as zeros.

The metrics are split across two gRPC services, one per scope. Worker
metrics describe the dataplane instance behind the gateway that answers,
and one gateway runs per instance, so a collector may record which
gateway it scraped them from:

    /controlplane.ynpb.v1.MetricsService/GetMetrics

Port metrics describe the hardware itself and read the same from every
gateway, so no gateway identity belongs on them:

    /controlplane.ynpb.v1.PortMetricsService/GetMetrics

## Worker metrics

One series per dataplane worker thread. Cumulative counters carry the
labels `worker_idx`, `core_id`, `device_id`, `queue_id` and count
monotonically since the counters were reset:

- `worker_iterations` (counter) — worker loop polls.
- `worker_rx_packets`, `worker_rx_bytes` — received from the NIC.
- `worker_tx_packets`, `worker_tx_bytes` — sent to the NIC.
- `worker_remote_rx_packets`, `worker_remote_tx_packets` — inter-worker
  traffic.
- `worker_local_tx_drops`, `worker_remote_tx_drops`, `worker_disposed` —
  drops by cause; `worker_disposed` already includes both drop counters.
- `worker_rx_bursts` (histogram) — RX poll size distribution.

### RX packet pool gauges

Gauges report the occupancy of each worker's RX mempool, the pool packet
allocation fails against when exhausted:

- `worker_rx_mempool_capacity` (gauge, objects) — total pool capacity,
  constant for the lifetime of the worker.
- `worker_rx_mempool_available` (gauge, objects) — objects free in the
  most recent dataplane sample.

Semantics:

- Labels: `instance_id`, `worker_idx`, `core_id`, `device_id`,
  `queue_id`. A pool's canonical identity is `(instance_id,
  worker_idx)`; device and queue describe the pool's ownership, the core
  its placement. The `instance_id` label appears only on the pool
  gauges: the cumulative worker counters above keep their historical
  label set so existing time series continue.
- Objects in use are `capacity - available`. That difference includes
  mbufs held in NIC RX/TX descriptors and packets in flight, so it is an
  allocator occupancy, not a packet backlog.
- `0 <= available <= capacity` always holds on a published snapshot; a
  violation fails collection of the whole worker family (see above)
  instead of being clamped.
- The owning worker resamples `available` at most once every 10 ms: a
  resample happens when a worker round finds the deadline expired, and
  slow rounds stretch the spacing further. A scrape between resamples
  returns the previous value, so short spikes may be missed.
- The same values are exposed per worker through the
  `controlplane.ynpb.v1.CountersService/Workers` RPC as the
  presence-bearing `rx_mempool` field. Compatibility across versions:
  a client newer than the server sees the field absent (`n/a` in the
  `RX pool free` column of `yanet-cli-counters workers`); a control
  plane newer than the dataplane gets a missing-counter error instead —
  the whole `Workers` call fails and `Collect()` omits the worker
  family — so a version skew can never masquerade as a real occupancy.

## Shared-memory allocator capacity

The built-in memory service exports these gauges through the instance
metrics endpoint, alongside the existing per-agent arena gauges:

| Metric | Value | Labels |
| --- | --- | --- |
| `memory_allocator_free_bytes` | Sum of the sizes of all free blocks, in bytes. | `scope`, `agent` |
| `memory_allocator_largest_free_block_bytes` | Largest currently free block, or zero when exhausted. | `scope`, `agent` |
| `memory_allocator_free_blocks` | Number of free blocks in one size class, not a cumulative histogram. | `scope`, `agent`, `block_size_bytes` |

`scope="cp", agent=""` identifies the shared control-plane allocator. It
backs execution contexts, counter registries, counter values and the
arenas reserved by agents. `scope="agent", agent="<name>"` identifies the
live generation of an agent's own allocator. Retired generations remain
visible through `memory_retired_arena_bytes`, without PID or generation
labels on the new series. A system agent without its own arena has zero
capacity here; its CP-backed allocations belong to the shared allocator.

Each allocator exposes all 24 size classes, from 8 bytes to 64 MiB,
including zero counts. The extra series count is 26 per allocator, with
no labels derived from configuration generations or memory-context paths.
Existing `memory_arena_free_bytes{agent}` series retain their labels and
values. The shared allocator excludes space already reserved by agents;
agent free capacity is not available to an unrelated shared CP request.

A snapshot takes the allocator lock for a bounded scan of its pool
counters, without walking free lists or allocating from shared memory.
The total, largest block and all class counts describe the same instant
within one allocator; different allocators are sampled independently.
Sizes are internal block sizes, including allocator rounding and ASan red
zones when enabled, rather than maximum usable payload sizes.

For a valid request rounded to a block size `B`, `free_bytes >= B` with
`largest_free_block_bytes < B` identifies insufficient large blocks
despite enough total free space. Compare that condition with the required
block size, not the unrounded request. A single fragmentation percentage
is misleading because even an unfragmented arena consists of blocks no
larger than 64 MiB. Scrapes can miss transient allocation peaks.

The same snapshots are available in `yanet-cli-inspect`: the summary shows
shared CP free capacity and the largest free block for CP and agents;
`--memory` adds every size class alongside the memory-context trees.
`--format json` includes `instance_info.cp_allocator` and each agent
instance's `allocator` irrespective of `--memory`. With an older server,
absent snapshots remain absent and the agent's largest block is `n/a`,
not an exhausted-allocator zero. Rebuild native info-library consumers
with the updated headers; the shared-memory layout is unchanged.

## Port metrics

One series per DPDK port and xstat counter, cumulative:

- `port_counter_value` (counter) — labels `port_id`, `port_name`,
  `counter`.

Every gateway answers with the same values, so any one of them can be
scraped and a gateway that is down costs nothing as long as another is.
Scraping several is equally correct only where the collector attaches
nothing that identifies the gateway it asked: a collector that labels a
series by its scrape target must either take this endpoint from one
gateway or drop that label, or a summed counter counts one port once per
gateway. That is what divides the two scopes — a series an aggregator
would sum belongs here.

Only the port endpoint serves them. A collector configured against the
instance endpoint alone gets no port series and no error saying so, and a
deployment that grants metrics access per method has to grant this one.

The same values are exposed per port through the
`controlplane.ynpb.v1.CountersService/Ports` RPC, which reports every port
from whichever instance is asked.

## Gateway TLS metrics

Both gauges come from the instance endpoint and hold unix times in
seconds, so an alert compares them with the current time:

- `gateway_tls_certificate_not_after_seconds` — labels `listener`
  (`grpcs` or `https`) and `source`, the `cert_file` of a `key_pairs`
  entry: when the certificate that listener serves for that entry expires.
  A scrape first rereads the entries due for a reload, so the series
  follows a renewal without waiting for a handshake.
- `auth_x509_client_certificate_not_after_seconds` — label `user`, the
  account name the x509 authenticator derives from a client certificate:
  the earliest expiry among the certificates that user presented on calls
  in the last hour. A certificate unused for an hour drops out, and a user
  left with none loses the series. It moves on to a renewed certificate's
  expiry only once every connection of that user presents the renewed
  one, so a client still holding an expiring certificate stays visible.
