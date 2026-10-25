lib_dependencies = [
  dependency('threads'),
  dependency('yaml-0.1'),
  # Headers only: the full DPDK dependency would copy every DPDK object
  # into libdataplane.a; the executable below links it instead.
  libdpdk_inc_dep,

  lib_common_dep,
  lib_logging_dep,
  lib_time_dp_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
  lib_worker_dp_dep,
  lib_counters_dep,
  lib_dp_zone_dep,
  lib_packet_dp_dep,
  lib_errors_dep,
]

lib_sources = files(
  'config.c',
  'dataplane.c',
  'device.c',
  'dpdk.c',
  'instance.c',
  'worker.c',
  'numa.c',
)

lib_dataplane = static_library(
  'dataplane',
  lib_sources,
  dependencies: lib_dependencies,
  install: false,
)

lib_dataplane_dep = declare_dependency(
  link_with: lib_dataplane,
  include_directories: include_directories('.'),
  dependencies: lib_dependencies,
)

dependencies = [
  dependency('threads'),
  dependency('yaml-0.1'),
  lib_common_dep,
  lib_logging_dep,
  libdpdk_dep,
  lib_dataplane_dep,

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
  lib_l3b_dp_dep,

  # objects
  # Startup resolves these constructors by dlsym on this executable, so they must be linked in and exported.
  lib_fwstate_objects_dep,
  lib_l3b_objects_dep,
  lib_ring_objects_dep,

  #devices
  lib_dev_plain_dp_dep,
  lib_dev_vlan_dp_dep,
  lib_dev_trafgen_dp_dep,
  lib_dev_vxlan_dp_dep,
]

sources = files(
  'main.c',
)

executable(
  'yanet-dataplane',
  sources,
  dependencies: dependencies,
  install: true,
  export_dynamic: true,
)

subdir('unittest')
