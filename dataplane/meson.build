lib_dependencies = [
  dependency('threads'),
  dependency('yaml-0.1'),
  libdpdk.get_variable('dpdk_dep'),

  lib_common_dep,
  lib_logging_dep,
  lib_pipeline_dp_dep,
  lib_time_dp_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
  lib_worker_dp_dep,
  lib_filter_query_dep,
]

lib_sources = files(
  'config.c',
  'dataplane.c',
  'device.c',
  'worker.c',
  'numa.c',
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
  lib_decap_dp_dep,
  lib_dscp_dp_dep,
  lib_acl_dp_dep,
  lib_forward_dp_dep,
  lib_mirror_dp_dep,
  lib_route_dp_dep,
  lib_route_mpls_dp_dep,
  lib_nat64_dp_dep,
  lib_pdump_dp_dep,
  lib_fwstate_dp_dep,
  lib_blackhole_dp_dep,
  lib_unrdup_dp_dep,

  # objects
  # Startup resolves these constructors by dlsym on this executable, so they must be linked in and exported.
  lib_fwstate_objects_dep,

  #devices
  lib_dev_plain_dp_dep,
  lib_dev_vlan_dp_dep,
  lib_dev_trafgen_dp_dep,
]

sources = files(
  'drivers/sock_dev.c',
  'dpdk.c',
  'main.c',
)

executable(
  'yanet-dataplane',
  sources,
  dependencies: dependencies,
  # In sanitizer builds the staged shared runtimes need the rpath on the
  # final executable too; link_args of the static libraries do not
  # propagate to the link of the executable itself.
  link_args: yanet_link_args,
  install: true,
  export_dynamic: true,
)

subdir('unittest')
