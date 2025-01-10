dependencies = []
dependencies += libdpdk.get_variable('dpdk_dep')

dependencies += lib_common_dep
dependencies += lib_pipeline_dp_dep

lib_sources = files(
  'dataplane.c',
  'device.c',
  'worker.c',
)

lib_dataplane = static_library(
  'dataplane',
  lib_sources,
  dependencies: dependencies,
  install: false,
)

lib_dataplane_dep = declare_dependency(
  link_with: lib_dataplane,
  include_directories: include_directories('.'),
)

dependencies += lib_dataplane_dep
dependencies += lib_module_dp_dep
dependencies += lib_pipeline_dp_dep
dependencies += lib_config_dp_dep

dependencies += lib_balancer_dp_dep
#dependencies += lib_decap_dp_dep
#dependencies += lib_acl_dp_dep
dependencies += lib_forward_dp_dep
dependencies += lib_route_dp_dep


sources = files(
  'main.c',
  'dpdk.c',
  'drivers/sock_dev.c')

executable(
  'yanet-dataplane',
  sources,
  dependencies: dependencies,
  install: true,
)

subdir('unittest')
