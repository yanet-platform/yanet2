dp_dependencies = [
  lib_common_dep,
  libdpdk_inc_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_forward_dp = static_library(
  'forward_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_forward_dp_dep = declare_dependency(
  link_with: lib_forward_dp,
  link_args: [
    '-Wl,--require-defined=new_module_forward',
    '-Wl,--export-dynamic-symbol=new_module_forward',
  ],
  dependencies: dp_dependencies,
)

dataplane_module_deps += lib_forward_dp_dep
