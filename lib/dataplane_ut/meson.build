dependencies = [
  lib_common_dep,
  lib_logging_dep,
  lib_errors_dep,
  lib_counters_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
  lib_agent_cp_dep,
  lib_worker_dp_dep,
  libdpdk_inc_dep,
]

sources = files(
  'dataplane_ut.c',
)

lib_dataplane_ut = static_library(
  'dataplane_ut',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args + ['-Wl,-E'],
  dependencies: dependencies,
  include_directories: yanet_rootdir,
  install: false,
)

lib_dataplane_ut_dep = declare_dependency(
  link_with: [lib_dataplane_ut],
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)
