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
| `shmalloc` | Block allocator and memory contexts of `common/memory*.h` | no |
| `agent` | `agent_attach`, agent registry, sweep of superseded agents | no |
| `cpconfig` | Module configurations, generation copy and upsert, workers' execution contexts, counter storages, publication, teardown of retired generations | no |
| `lpm` | Prefix trees of `common/lpm.h` | no |
| `decap` | Decap module configurations | no |
| `cmd/yanet-native` | Static inspector of a running dataplane | no |
| `internal/coracle` | Runs the C implementations as the tests' oracle | tests only |
| `internal/nativetest` | In-process dataplane fixture, the C-then-Go comparison, the test assigner | tests only |

Tests: `make test-only GO_TEST_PACKAGES="./controlplane/native/..."`. The production
packages build with `CGO_ENABLED=0`. The layouts are generated from an amd64 dataplane
only, so the packages that use them build on linux/amd64 only. The tests that compare
with C do not build under `-asan`: AddressSanitizer gives the C allocator red zones the
Go allocator does not mirror.

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
and `decap`.

| Operation | cgo | native |
|---|---|---|
| Build a decap configuration of 256 prefixes | 36.7 µs | 12.2 µs |
| Build and publish it until every worker runs it | ~48 µs | ~37 µs |
| Build and publish one blackhole configuration | 7.8 µs | 14.4 µs |

- Building per item through cgo pays a call per prefix; native code pays none.
- The in-process dataplane's C publish delivers contexts to the workers itself, on its
  thread; the native publish leaves that to an assigner thread, as a real dataplane's
  workers take them on their own cores. The handoff costs the native column 3 µs; with
  delivery on the publishing thread the blackhole publish is 11.0 µs, 6.3 µs of it the
  workers' execution contexts. Built from the same generation on one thread with warm
  memory, those take 5.2 µs in C and 6.7 µs in Go.
- Allocations per blackhole publish: 54 small objects, 696 bytes.

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

- A replaced module configuration stays in its agent's memory until the agent frees it;
  Go does not port the per-module free yet, and neither does it free functions or
  pipelines whose last reference goes, which a module update never does.
- The in-process dataplane's assigner takes a pthread mutex in shared memory; the tests
  reach it through cgo, a production dataplane does not need it.
- Modules beyond blackhole and decap need their configuration builders ported, each
  against its C api with the same comparison.
- Byte-identical results cover the topologies the tests build: one device, blackhole and
  decap modules, no objects, no runtime counter registries and no module device links.
- Error paths are not compared with C. A failed update frees its new generation, but a
  registry copy that fails part-way, or a worker's context that fails part-way, leaves
  what it allocated; several context allocations are not checked for exhaustion.
- A generation an agent pins while attaching is never freed if C retires it meanwhile.
- The layout test checks the generated views against the DWARF they came from; nothing
  checks at run time that a dataplane was built from the same headers.
