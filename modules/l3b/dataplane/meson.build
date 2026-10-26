dp_dependencies = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_l3state_dep,
  lib_statemap_dep,
  libdpdk_inc_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_l3b_dp = static_library(
  'l3b_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_l3b_dp_dep = declare_dependency(
  link_with: lib_l3b_dp,
  dependencies: dp_dependencies,
  link_args: [
    '-Wl,--require-defined=new_module_l3b',
    '-Wl,--export-dynamic-symbol=new_module_l3b',
  ],
)

dataplane_module_deps += lib_l3b_dp_dep
