lib_dependencies = [
  dependency('threads'),
  dependency('yaml-0.1'),
  libdpdk.get_variable('dpdk_dep'),

  lib_common_dep,
  lib_logging_dep,
  lib_pipeline_dp_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
]

lib_sources = files(
  'config.c',
  'dataplane.c',
  'device.c',
  'worker.c',
)

lib_dataplane = static_library(
  'dataplane',
  lib_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: lib_dependencies,
  install: false,
)

lib_dataplane_dep = declare_dependency(
  link_with: lib_dataplane,
  include_directories: include_directories('.'),
)

dependencies = lib_dependencies + [
  lib_dataplane_dep,
  lib_module_dp_dep,

  # modules
  lib_balancer_dp_dep,
  lib_decap_dp_dep,
  lib_dscp_dp_dep,
  # lib_acl_dp_dep,
  lib_forward_dp_dep,
  lib_route_dp_dep,
  lib_nat64_dp_dep,
  lib_pdump_dp_dep,
]

sources = files(
  'drivers/sock_dev.c',
  'dpdk.c',
  'main.c',
)

if not get_option('fuzzing').enabled()
  executable(
    'yanet-dataplane',
    sources,
    dependencies: dependencies,
    install: true,
  )
endif

subdir('unittest')