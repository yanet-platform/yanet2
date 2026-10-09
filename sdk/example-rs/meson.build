# Reference out-of-tree YANET module with a Rust dataplane, built only
# through the module SDK.
#
# This is a standalone meson project: the yanet2 tree never traverses it.
# The dataplane is the Rust crate in rust/ (cargo builds it as the plugin
# the runtime loader picks up from plugin_dir, libexample_rs_dp.so — see
# the Makefile); meson builds only the control-plane FFI archive the Go
# bindings link. Copy this directory to start a new Rust module; see
# docs/module-sdk.md and docs/rust-dataplane-modules.md.
project(
  'yanet-module-example-rs',
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
# carries the yanet and DPDK include paths and the shared defines.
yanet_sdk = dependency('yanet-module-sdk')

subdir('api')
