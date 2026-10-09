# Control-plane FFI archive consumed by the Go bindings through their #cgo
# -L/-l flags. Its unresolved agent/config symbols resolve against the
# libraries the yanet2 controlplane/ffi package links in the same binary.
#
# The archive lands in its own build/api/ directory so that the -L path
# the Go bindings use never contains a shared object, keeping it from
# ever shadowing an in-tree static module library at link time.
static_library(
  'example_rs_cp',
  files('controlplane.c'),
  dependencies: [yanet_sdk],
  install: false,
)
