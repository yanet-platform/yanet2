// Package layout holds Go views of the C structures YANET keeps in shared
// memory, which the build generates with shmgen.
//
// The meson target native-layout compiles c/types.c, which includes the
// headers that define the structures as the dataplane compiles them, and
// generates the views from its DWARF into layout_<arch>.go, with a test
// against the same DWARF, as the protobuf packages are generated: `make
// native-layout`, and every target that prepares the Go packages, runs it.
//
// The views apply only to the architecture built, one file per
// architecture: the layouts differ, arm64 builds align to 128-byte cache
// lines where amd64 builds align to 64.
package layout
