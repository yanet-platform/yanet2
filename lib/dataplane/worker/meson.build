dependencies = [
  lib_common_dep,
  lib_config_dp_dep,
  libdpdk_inc_dep,
]


sources = files(
  'worker.c',
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
  link_with: lib_worker_dp,
  include_directories: yanet_libdir,
)
