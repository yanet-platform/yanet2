dependencies = []
dependencies += dependency('threads')
dependencies += dependency('libpcap')

dependencies += libdpdk_dep

dependencies += lib_common_dep
dependencies += lib_module_dp_dep
dependencies += lib_config_dp_dep
dependencies += lib_dataplane_dep

dependencies += lib_agent_cp_dep

#dependencies += lib_acl_dp_dep
#dependencies += lib_decap_dp_dep
#dependencies += lib_balancer_dp_dep
dependencies += lib_route_dp_dep
dependencies += lib_route_cp_dep
#dependencies += lib_forward_dp_dep

sources = files(
  'main.c',
  '../../dpdk.c',
)

executable(
  'unittest-dpdk',
  sources,
  include_directories: [yanet_rootdir, '.', '../..'],
  dependencies: dependencies,
)
