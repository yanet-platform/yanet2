# Ring objects reference

A ring is a standalone shared-memory object. It holds one buffer of opaque
records per dataplane worker. Each worker's buffer sits on its own cache
lines. When a buffer is full, the writer drops the oldest whole records to
make room. A module config can link a ring by name. The ring has no dataplane module of
its own.

## Ownership

- A ring is a standalone `cp_object` of type `"ring"`. `RingService`
  creates and deletes it. The pdump module control plane hosts that
  service.
- No ring is created implicitly. Every ring exists because a `create` call
  named it.
- `create` rejects a name that already exists. If you delete a name and
  create it again, you get a new object with a new handle. A lease taken
  on the old handle never matches the new one.
- Delete is refused in two cases. First, a consumer holds a lease on the
  ring (the Go owner checks this). Second, a published module config still
  links the ring by name (the generic `cp_object` check that every shared
  object gets). In both cases the ring stays usable. The caller retries the
  delete after the blocker is gone. No production module links a ring
  yet, so today only tests can hit the link refusal.

## Limitations

- After a control-plane restart, `RingService` does not pick up the rings
  that are still in shared memory. `list` and `show` do not see them.
  `delete` reports not found. `create` with the same name reports that the
  ring already exists. Module shutdown does not free these rings either.
  The fwstate map service has the same limitation. Both are tracked as
  follow-up work.

## Capacity

- Capacity is per worker. Each worker gets its own buffer of the
  configured size. There is no shared pool.
- Capacity must be a power of two. The smallest value is the 8-byte record
  frame. The largest is the block allocator's maximum block size, 64 MiB.
  Under ASan that maximum is 64 MiB minus two red zones, so the largest
  accepted capacity is 32 MiB. The red-zone padding also moves each
  allocation into the next size class. So in practice an ASan build
  already fails with an out-of-memory error above about 16 MiB. The exact
  point depends on how much free space is left in the owning agent's
  arena.
- Capacity is set at `create` and never changes. To get a different
  capacity, create a new ring.
- The number of per-worker buffers is not a `create` argument and is not
  reported. It always equals the dataplane's configured worker count.
- Ring storage is charged to the pdump module's agent memory. Storage is
  each worker's buffer (capacity times worker count) plus the metadata
  array. The limit is `memory_requirements` in the controlplane config,
  16 MiB by default. A create that would go over it fails.

## Overwrite semantics

- Each worker has exactly one writer. The writer never blocks. When the
  buffer is full, the writer evicts the oldest whole records to make room.
  It does not stall and does not fail the write.
- Eviction always drops whole records. It never cuts a record in half.
- Eviction frees space in chunks. When a record does not fit, the writer
  drops the oldest records until at least one chunk is free, or the
  record's own length if that is larger. It stops at a record boundary.
  It then publishes only the final position, once.
- The chunk is a sixteenth of the capacity, at most 4 KiB, rounded down to
  a multiple of 4 bytes. It is fixed at `create`. The next records that
  fit in the freed space are written without another eviction.
- The writer reads the frames of the oldest records ahead of time. It
  reads one frame on each write that needs no eviction. It reads up to one
  chunk past the readable position. So when an eviction comes, the writer
  already knows where the chunk ends.
- If the writer finds a corrupt length in the next record it would evict,
  it does not guess how far it is safe to move. It drops every published
  record in one step.
- Each reader keeps its own cursor per worker. A slow reader loses the
  records that the writer evicted before the reader got to them. A slow
  reader never slows down the writer or other readers.

## Framing and seqno

- The frame is little-endian. The Go reader decodes it as little-endian.
  The C header does not compile on a big-endian target.
- Every record starts with an 8-byte frame, `{total_len: u32, seqno:
  u32}`. The opaque payload follows the frame. `total_len` counts the
  frame and the payload together.
- Records start on a 4-byte boundary. Only this 4-byte alignment is
  guaranteed. So a private payload header (for example, a future capture
  header) must still decode itself safely.
- `seqno` is a per-worker u32 counter. The writer assigns it at commit and
  adds one for every accepted record. It wraps from `0xFFFFFFFF` back to
  `0` with no gap.
- The writer rejects a record whose declared size is below the frame size
  or above the batch limit (the capacity minus the eviction chunk). For a
  rejected record nothing is written, no position moves, and no seqno is
  used.

## Batch publication

- The writer commits a record and publishes it as two separate steps.
- A commit writes the record's frame, stamps its seqno and moves the
  writer's private write position. Readers do not see the record yet.
- A publication makes visible every record committed since the last
  publication. It does this with one store of the published write
  position. If nothing was committed, it does nothing.
- Each ring has a publish batch. It is 8 records unless `create` asks for
  another value, from 1 to 1024. It is fixed at `create`. When a commit
  brings the number of unpublished records up to the publish batch, that
  commit publishes them itself. So a steady producer stores to the
  published cache line once per batch, not once per record. A reader that
  polls that line takes it away from the writer's CPU at most once per
  batch.
- A producer also publishes once at the end of each call or burst. The
  publish batch only limits how many records wait unpublished while the
  producer keeps writing. It never holds back the last records of a call.
  So the last records of a slow producer reach readers without waiting for
  the batch to fill.
- A batch also has a byte limit: the capacity minus the eviction chunk,
  counted in aligned record lengths. If a record would take the batch past
  this limit, the writer first publishes the records committed so far, the
  same way an explicit publication does. Then that record starts a new
  batch. This happens no matter how few records the batch holds. Going
  past the limit is never an error. The writer reports how much room is
  left in the current batch, so a producer can use it to size its batches.
- A batch never evicts its own records. Eviction drops only published
  records. Within the byte limit, evicting all published records always
  frees a whole chunk. So no record is lost to its own batch, and seqnos
  stay contiguous.
- Everything below the published write position is whole records, fully
  written to memory. So a reader can read right up to that position.

## Metadata layout

Each worker's metadata (`struct ring_worker`) takes four cache lines of
`YANET_CACHE_LINE_SIZE` (L) bytes. A reader polls the positions on one
line, and the writer updates its per-record state on another. So the
reader does not fight the writer for the same line:

| Line (offset) | Fields (offset within the line, bytes) | Written by | Read by |
|---|---|---|---|
| Writer-private `local` (0) | `write_idx` (0), `readable_idx` (8), `published_write_idx` (16), `evict_idx` (24), `data` (32), `next_seqno` (40), `size` (44), `mask` (48), `evict_chunk` (52), `publish_batch` (56), `batch_records` (60) | writer, on every record | writer; readers load `size`, `mask`, `data` once when they attach |
| Guard (L) | unused | — | — |
| Published `published` (2L) | `write_idx` (0), `readable_idx` (8) | writer, release stores only | readers |
| Guard (3L) | unused | — | — |

The writer keeps its own copy of the positions in the private line. It
never loads the published line. After it updates its own copy, it
release-stores the new value to the published line.

The offset of the published line grows with L. So the Go reader does not
read the struct through its own CGo view of it, because cgo may build the
reader with another L than the C archive. It calls
`ring_object_worker_view` in the archive, which returns the addresses of
the published positions and the data area together with `size` and
`mask`. Only C code built with the archive's L reads `struct ring_worker`
directly.

The private fields mean the following:

- `published_write_idx` is the last write position the writer published.
  The unpublished batch starts there.
- `batch_records` counts the records in that batch. The writer compares
  it with `publish_batch`.
- `evict_idx` is the record boundary that the writer has already read
  ahead to, for the next eviction.

Some CPUs prefetch cache lines in adjacent pairs. The guard lines make
sure such a CPU never loads a line that a reader polls together with a
line that the writer stores to. This holds within one worker and between
neighbouring workers in the array.

## Ordering contract

The writer (`common/ring.h`) is the only code that changes either
position. It uses no lock and no read-modify-write. For each record:

1. If the record would take the unpublished batch past the byte limit,
   publish the batch first, the same way as step 7. Do this before any
   eviction and before writing any byte of the record. This is rare: at
   most once per batch limit of bytes.
2. Read the private `write_idx` and `readable_idx` with plain loads. If
   the record fits, read at most one more published record's frame ahead
   for the next eviction (reads only), and go to step 5.
3. Otherwise, keep walking over the oldest whole published records until a
   chunk is free. On a corrupt length, jump to the start of the batch.
   Update the private `readable_idx`. Publish the final boundary with one
   release store of the published `readable_idx`.
4. Issue a release fence before writing over any evicted byte. Steps 3 and
   4 run only when something was evicted, so once per chunk, not once per
   record.
5. `memcpy` the frame and the payload.
6. Move the private `write_idx` forward (commit). If the unpublished
   records now fill the publish batch, publish them as in step 7.

For each batch, when the publish batch is full and at the end of each
producer call:

7. Publish every committed record with one release store of the published
   `write_idx`. Skip it when nothing was committed.

`readable_idx` moves once per eviction chunk, not once per evicted record.
Do not use it to count evictions.

The reader (`objects/ring/bindings/go/cring`) works like this:

1. It acquire-loads both published positions.
2. It copies data up to the published `write_idx` into a private buffer.
3. It moves its cursor forward with `atomic.Add`.
4. It acquire-loads `readable_idx` again.
5. If that recheck shows that the writer evicted part of the copied data,
   it drops that prefix before parsing.

Steps 3 and 4 must stay atomic and in this order.

**Why it is correct.**

Publication. The writer stores every frame and payload byte of the batch
(and of all earlier batches) before the release store of `write_idx`. So a
reader whose acquire load sees the new `write_idx` also sees every byte
below it. The publications in steps 1 and 6 are the same store. Each of
them happens when every committed record is whole and the next record has
no byte written yet. So the same argument covers them. Unpublished records
lie at or past the published `write_idx`, and no reader copies there.

Eviction. If the reader copied a byte that the writer then overwrote, the
reader's recheck must see the eviction that covers that byte. One
`readable_idx` store and one fence cover a whole chunk, because the writer
overwrites the chunk's bytes only after both. The walk stops at the start
of the batch. So a write inside the current batch only lands on bytes that
an already published eviction has marked invalid.

On arm64, the writer's fence keeps the store of the published
`readable_idx` before the data stores. On the reader side, the add has a
release half and the reload is an acquire. A release followed by an
acquire is never reordered, so the copy's loads finish before the
recheck.

On x86-64, the CPU keeps stores in program order and loads in program
order (TSO). So no fence instruction is needed.

| Writer | Reader | x86-64 | arm64 |
|---|---|---|---|
| Release store of published `readable_idx`, once per chunk | Acquire recheck | `mov` / `mov` | `stlr` / `ldar` |
| Release fence, once per chunk | Release half of the cursor add | none / `lock xadd` | `dmb ish` (or `dmb ishld` + `dmb ishst`) / `ldaddal` or `ldaxr`+`stlxr` |
| `memcpy` of every record in the batch | `copy` | plain | plain |
| Release store of published `write_idx`, once per batch | Acquire snapshot | `mov` / `mov` | `stlr` / `ldar` |

**Compiler dependency.** GCC and clang treat `atomic_thread_fence` as a
full compiler barrier. So the compiler does not move the `memcpy` stores
above it, even on x86-64 where it emits no instruction.

**Not claimed.** The bulk copies are ordinary memory accesses. They race
with each other on purpose. This code is not data-race-free in the ISO C
sense. Its correctness rests on two things: the writer marks whole records
invalid before it overwrites them, and the reader copies first and
rechecks after.

## Performance

The table shows the writer cost per record and the reader throughput at
64-byte records. Each design step is added on top of the previous one. The
baseline is the pdump writer that this ring replaces. "Alone" means the
writer runs with no reader. "Full reader" means one reader per writer runs
at the same time and copies and parses every record.

| Design step | x86-64 alone, ns | x86-64 full reader, ns | arm64 alone, ns | arm64 full reader, ns | x86-64 reader, Mrec/s | arm64 reader, Mrec/s |
|---|---|---|---|---|---|---|
| Baseline: pdump writer | 34.5 | 151 | 36.2 | 307 | 6.6 | 3.25 |
| 1. Single-writer publication, each worker on its own cache lines, one readable store per eviction, eviction fence | 13.0 | 59 | 22.3 | 130 | ~17 | ~7.7 |
| 2. Separate writer-private and published cache lines, with guard lines | 13.3 | 45 | 22.7 | 40 | — | 24.8 |
| 3. Batch publication (default 8) and chunked eviction | 12.8 | 24.6 | 23.0 | 31.6 | 40.6 | 31.6 |

Test setup. x86-64: Xeon Gold 6230 under KVM, pinned to 4 vCPUs. arm64:
Cortex-A76 with 128-byte cache lines. 1 MiB ring, 64-byte records, no
pacing, one worker. To reproduce the last row, run
`build/tests/common/ring_bench` (`RING_BENCH_CPUS`, `RING_BENCH_REPS`). The
numbers are only a guide. They change with hardware, compiler and load.

## Pdump capture status

Pdump packet capture still writes into its own private ring buffers
inside the module. It does not use standalone ring objects yet. A later
change moves it over.

## CLI examples

`yanet-cli-ring` manages ring objects through `RingService`. The
`--format json` flag comes from the shared `ync` CLI framework. It
switches any of these commands from human-readable output to the JSON
response.

Create a ring with a 1 MiB capacity per worker and the default publish
batch. The second command creates a ring that publishes every 32 records:

```bash
yanet-cli-ring create --name captures --capacity 1MiB
yanet-cli-ring create --name bursts --capacity 1MiB --publish-batch 32
```

List every registered ring, sorted by name, as a
`NAME`/`CAPACITY`/`PUBLISH BATCH` table:

```bash
yanet-cli-ring list
```

Show the capacity and publish batch of one ring:

```bash
yanet-cli-ring show --name captures
```

Delete a ring:

```bash
yanet-cli-ring delete --name captures
```

Every subcommand except `list` takes `--name`/`-n` to pick the ring.

`--capacity` takes a size in bytes or in IEC units, such as `4096`,
`64KiB` or `1MiB`. The CLI rejects a value that is not a power of two.
This includes decimal units such as `1MB`. The service checks the range
described above before it creates anything.

`--publish-batch` takes 1 to 1024 records. If it is not given, the
service uses its default of 8.
