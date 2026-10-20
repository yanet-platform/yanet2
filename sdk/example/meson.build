# Reference out-of-tree YANET module, built only through the module SDK.
#
# This is a standalone meson project: the yanet2 tree never traverses it.
# It compiles the dataplane plugin the runtime loader picks up from
# plugin_dir (libexample_dp.so) and the control-plane FFI archive the Go
# bindings link. Copy this directory to start a new module; see
# docs/module-sdk.md.
project(
  'yanet-module-example',
  'c',
  meson_version: '>= 0.61',
  default_options: [
    'buildtype=release',
    'warning_level=2',
    'werror=true',
    'optimization=2',
  ],
)

# Resolves through PKG_CONFIG_PATH pointing at the yanet2 build directory
# (the repository Makefile and this directory's Makefile both set it): it
# carries the yanet and DPDK include paths, the shared defines, and the
# ABI-version stamp archive.
yanet_sdk = dependency('yanet-module-sdk')

# The plugin the dataplane dlopen's from plugin_dir. Undefined references
# to yanet and DPDK symbols are left unresolved here on purpose: the
# dataplane binary exports them dynamically and satisfies them at load
# time, which is why no yanet library is linked.
shared_module(
  'example_dp',
  files('dataplane/dataplane.c'),
  dependencies: [yanet_sdk],
  install: false,
)

# Control-plane FFI archive consumed by the Go bindings through their #cgo
# -L/-l flags. Its unresolved agent/config symbols resolve against the
# libraries the yanet2 controlplane/ffi package links in the same binary.
static_library(
  'example_cp',
  files('api/controlplane.c'),
  dependencies: [yanet_sdk],
  install: false,
)
