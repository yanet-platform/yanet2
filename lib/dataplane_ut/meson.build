dependencies = [
  # The harness loads module plugins.
  dependency('dl'),
  lib_common_dep,
  lib_logging_dep,
  lib_errors_dep,
  lib_counters_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
  lib_agent_cp_dep,
  lib_worker_dp_dep,
  lib_lib_utils_dep,
  libdpdk_inc_dep,
]

sources = files(
  'dataplane_ut.c',
  'stages.c',
  'tx_pipe.c',
  'tx_stage.c',
)

lib_dataplane_ut = static_library(
  'dataplane_ut',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_dataplane_ut_dep = declare_dependency(
  link_with: [lib_dataplane_ut],
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)
