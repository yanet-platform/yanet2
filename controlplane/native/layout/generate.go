// Package layout holds Go views of the C structures YANET keeps in shared
// memory, which the build generates with shmgen.
//
// The meson target native-layout, which `make native-layout` and every
// target that prepares the Go packages run, compiles c/types.c from the
// headers that define the structures, with the project's arguments, and
// generates the views from its DWARF into layout_<arch>.go, with a test
// against the same DWARF, as the protobuf packages are generated. The views
// apply only to the architecture built: arm64 builds align to 128-byte
// cache lines where amd64 builds align to 64. The same compilation gives
// the dataplane ready magic, the module ABI version and the block
// allocator's geometry the headers define, as typed constants.
//
// The structures reach DPDK through pointers only, which the views keep
// opaque, so DPDK's headers take no part in the layouts.
package layout
