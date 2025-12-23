cp_dependencies = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  libdpdk_inc_dep,
]

includes = include_directories('../dataplane', '../../..')

sources = files(
  'fwstate_cp.c',
)

lib_fwstate_cp = static_library(
  'fwstate_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_fwstate_cp_dep = declare_dependency(
  link_with: [lib_fwstate_cp],
)
