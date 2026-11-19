dp_dependencies = [
  lib_common_dep,
  lib_fwstate_objects_dep,
  lib_nat64_packet_dep,
  lib_statemap_dep,
  libdpdk_inc_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_nat64s_dp = static_library(
  'nat64s_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_nat64s_dp_dep = declare_dependency(
  link_with: lib_nat64s_dp,
  dependencies: dp_dependencies,
  link_args: [
    '-Wl,--require-defined=new_module_nat64s',
    '-Wl,--export-dynamic-symbol=new_module_nat64s',
  ],
)

dataplane_module_deps += lib_nat64s_dp_dep
