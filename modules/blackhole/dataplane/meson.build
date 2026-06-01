dp_dependencies = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_module_dp_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_blackhole_dp = static_library(
  'blackhole_dp',
  dp_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dp_dependencies,
  install: false,
)

lib_blackhole_dp_dep = declare_dependency(
  link_with: lib_blackhole_dp,
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_module_blackhole=new_module_blackhole',
    '-Wl,--export-dynamic-symbol=new_module_blackhole',
  ],
)
