# Native control plane (proof of concept)

The control plane reads and writes YANET's shared memory from Go, without cgo. The C
layouts come from the DWARF of the dataplane binary; the C algorithms that shape shared
memory (allocator, registries, indexes, hashes) are ported and tested against C byte for
byte.

## Packages

| Package | Role | cgo |
|---|---|---|
| `cmd/shmgen` | Generates Go views of C structures from DWARF, with a layout test | no |
| `layout` | The generated views (`go generate`), per architecture | no |
| `shm` | Relative pointers, process-local words, byte and pid locks, C strings | no |
| `zone` | Maps the storage, finds instances, pins generations, reads topology, modules, agents | no |
| `shmalloc` | Block allocator and memory contexts of `common/memory*.h` | no |
| `agent` | `agent_attach`, agent registry, sweep of superseded agents | no |
| `cpconfig` | `cp_module_init`, counter registry, string index, module configurations | no |
| `cmd/yanet-native` | Static inspector of a running dataplane | no |
| `internal/coracle` | Runs the C implementations as the tests' oracle | tests only |
| `internal/nativetest` | In-process dataplane fixture and the C-then-Go comparison | tests only |

Tests: `make test-only GO_TEST_PACKAGES="./controlplane/native/..."`. The production
packages build with `CGO_ENABLED=0`.

## How Go is held to C

`nativetest.Fixture.CompareWithC` clones the whole storage, runs an operation through C,
keeps the result, restores the clone and runs the same operation through Go. Both start
from the same bytes at the same addresses, so the allocator makes the same choices and the
storages must end byte-identical. The allocator is also compared on separate arenas,
interleaved with C on one allocator, and fuzzed with C as the oracle.

## Results so far

- Generated views match DWARF for every size and offset; compilation units that disagree
  about a structure fail the generation.
- A static binary attaches to a dataplane's storage as a separate process and reports
  what the cgo control plane reports.
- 1000 goroutines waiting for the configuration lock: inside cgo calls they hold 1012 OS
  threads, natively 34 (GOMAXPROCS 32).
- Allocator, memory contexts, agent attach and module configurations built from Go are
  byte-identical to C.

## Facts the port depends on

- The string hash of `common/str_index.h` hashes the last one to three bytes in reverse
  order; registry hashes fold 64 bits to 32 with `h ^ h >> 32`, seed `0x9e3779b9`, and a
  module's key hashes its name before its type.
- `strtcpy` writes the string and its terminator only; the rest of the field keeps its
  bytes.
- Some pointers in shared memory are absolute addresses of the dataplane process without
  an `abs_` prefix (`dp_worker` counters, `rx_mempool`); the generator takes them from
  `-local` until the headers mark them.
- Atomic fields are plain fields in C (`ready_magic`, `config_lock`); nothing in the
  binary says they are accessed atomically.
- The two spinlocks of `cp_config` share one 32-bit word; Go locks its byte through
  atomic OR and AND on the word.

## Next

Publishing a generation: registry copy and replace with their hash index, module upsert
and counter linking, the per-worker execution contexts, counter storages, publication,
the wait for workers and the release of the old generation.
