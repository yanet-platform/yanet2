# Native control plane (proof of concept)

The control plane reads and writes YANET's shared memory from Go, without cgo. The C
layouts come from the DWARF of the dataplane binary; the C algorithms that shape shared
memory (allocator, registries, indexes, hashes, prefix trees) are ported and tested
against C byte for byte.

## Packages

| Package | Role | cgo |
|---|---|---|
| `cmd/shmgen` | Generates Go views of C structures from DWARF, with a layout test | no |
| `layout` | The generated views (`go generate`), per architecture | no |
| `shm` | Relative pointers, process-local words, byte and pid locks, C strings | no |
| `zone` | Maps the storage, finds instances, pins generations, reads topology, modules, agents | no |
| `shmalloc` | Block allocator and memory contexts of `common/memory*.h` | `-asan` builds only |
| `agent` | `agent_attach`, agent registry, sweep of superseded agents | no |
| `cpconfig` | Module configurations and objects and their frees, generation copy, upsert and delete, workers' execution contexts, counter storages, publication, teardown of retired generations | no |
| `lpm` | Prefix trees of `common/lpm.h` | no |
| `decap` | Decap module configurations | no |
| `cmd/yanet-native` | Static inspector of a running dataplane | no |
| `internal/coracle` | Runs the C implementations as the tests' oracle | tests only |
| `nativetest` | In-process dataplane fixture, the C-then-Go comparison, the test assigner | tests only |

Tests: `make test-only GO_TEST_PACKAGES="./controlplane/native/..."`. The production
packages build with `CGO_ENABLED=0`. Layouts are generated per architecture, from an amd64
and an arm64 dataplane, and differ where structures align to cache lines (64 bytes on
amd64, 128 on arm64); the packages build on linux/amd64 and linux/arm64.

Under `-asan` the allocator does what the C one does in an AddressSanitizer build: red
zones around every block and the same poisoning of free blocks and link words, through
the sanitizer's interface. The comparisons with C then also require Go to leave exactly
the runs of memory unaddressable that C leaves, and Go's own reads and writes of shared
memory are checked against them.

## How Go is held to C

`nativetest.Fixture.CompareWithC` clones the whole storage, runs an operation through C,
keeps the result, restores the clone and runs the same operation through Go. Both start
from the same bytes at the same addresses, so the allocator makes the same choices and the
storages must end byte-identical. The allocator is also compared on separate arenas,
interleaved with C on one allocator, and fuzzed with C as the oracle. Single-field
mutations of the Go code fail these comparisons.

## Results

- Generated views match DWARF for every size and offset; compilation units that disagree
  about a structure fail the generation.
- A static binary attaches to a dataplane's storage as a separate process and reports
  what the cgo control plane reports.
- 1000 goroutines waiting for the configuration lock: inside cgo calls they hold 1012 OS
  threads, natively 34 (GOMAXPROCS 32).
- Byte-identical to C: allocator, memory contexts, agent attach, module configurations,
  generation copy, module upsert, every worker's execution contexts, and a whole module
  update (`cp_config_update_modules`) including the teardown of the replaced generation.
- The dataplane runs what Go publishes: a blackhole configuration published from Go
  drops packets on contexts Go built, and its counters continue from the generation C
  published.
- A decap configuration whose prefix tree Go built is byte-identical to the C decap api's
  and, published from Go, changes which tunnels the dataplane decapsulates.

## Numbers

In-process dataplane, two workers, AMD Ryzen AI MAX+ 395; `go test -bench` in `cpconfig`
and `decap`, medians of seven runs. Both columns deliver the workers' contexts on the
publishing thread, as the C publish does for an in-process dataplane, and both leave the
same bytes.

| Operation | cgo | native |
|---|---|---|
| Build a decap configuration of 256 prefixes | 35.9 µs | 11.8 µs |
| Build and publish it until every worker runs it | 46.2 µs | 18.8 µs |
| Build and publish one blackhole configuration | 7.9 µs | 6.5 µs |

- Building per item through cgo pays a call per prefix; native code pays none.
- About half of a C publish goes to the counter storages' tag sets: every storage of
  every worker is found and registered under up to 16 fixed 160-byte tags, encoded,
  sorted and hashed anew on each publish. Go writes the same tags, but encodes each path's
  set once and remembers it, since the names along a path do not change.
- A publish allocates and frees about 140 blocks of shared memory. C takes the allocator
  lock for each; Go takes it once to build the generation and once to release the old
  one, allocating the same blocks in the same order.
- Delivered by an assigner thread instead, as a real dataplane's workers take contexts
  on their own cores, the native blackhole publish takes 10.7 µs.
- Allocations per blackhole publish: 46 small objects, 624 bytes.

## Facts the port depends on

- The string hash of `common/str_index.h` hashes the last one to three bytes in reverse
  order; registry hashes fold 64 bits to 32 with `h ^ h >> 32`, seed `0x9e3779b9`, and a
  module's key hashes its name before its type.
- A counter storage registers under its tags sorted by key; the hash starts from the tag
  count as eight little-endian bytes.
- `strtcpy` writes the string and its terminator only; the rest of the field keeps its
  bytes.
- Free order is layout: the allocator's free lists decide where the next block lands.
- Some pointers in shared memory are absolute addresses of the dataplane process without
  an `abs_` prefix (`dp_worker` counters, `rx_mempool`); the generator takes them from
  `-local` until the headers mark them. One `abs_` field is stored relative
  (`module_ectx.abs_runtime_counter_storages`); the generator takes it from `-relative`.
- Atomic fields are plain fields in C (`ready_magic`, `config_lock`); nothing in the
  binary says they are accessed atomically.
- The two spinlocks of `cp_config` share one 32-bit word; Go locks its byte through
  atomic OR and AND on the word.
- `counter_tag_init` wrote uninitialised stack bytes into shared memory; the comparison
  with Go found it, and C now zeroes the tag.

## Limits

- A replaced module configuration or object is freed by its owner once no generation
  holds it, the module's own builder freeing its body; Go does not free functions or
  pipelines whose last reference goes, which a module update never does.
- The in-process dataplane's assigner takes a pthread mutex in shared memory; the tests
  reach it through cgo, a production dataplane does not need it.
- Modules beyond blackhole and decap need their configuration builders ported, each
  against its C api with the same comparison.
- Byte-identical results cover the topologies the tests build: one device, blackhole and
  decap modules, ring objects with counters and links, deletes and frees of modules and
  objects; no runtime counter registries and no module device links.
- Error paths are not compared with C. A failed update frees its new generation, but a
  registry copy that fails part-way, or a worker's context that fails part-way, leaves
  what it allocated; several context allocations are not checked for exhaustion.
- A generation an agent pins while attaching is never freed if C retires it meanwhile.
- Whether blocks carry red zones is decided by how Go is built, which matches the C side
  in one process. A separate Go process attached to an AddressSanitizer build of the
  dataplane needs the dataplane to publish its red zone in shared memory.
- The layout test checks the generated views against the DWARF they came from; nothing
  checks at run time that a dataplane was built from the same headers.
