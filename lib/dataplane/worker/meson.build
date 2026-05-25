dependencies = [
  lib_common_dep,
  lib_counters_dep,
  lib_logging_dep,
  lib_errors_dep,
  lib_config_dp_dep,
  lib_pipeline_dp_dep,
  lib_module_dp_dep,
  libdpdk_inc_dep,
]


sources = files(
  'pipeline_round.c',
  'worker.c',
  'counters.c',
)

lib_worker_dp = static_library(
  'worker_dp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_worker_dp_dep = declare_dependency(
  link_with: [lib_worker_dp],
  include_directories: yanet_libdir,
  dependencies: dependencies,
)
