test_includes = [
  yanet_rootdir,
  '../..', 
]

lookup_vs_test_sources = files(
  'lookup_vs.c', 'utils.c'
)

lookup_vs_exe = executable(
  'lookup_vs',
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  sources: lookup_vs_test_sources,
  include_directories: test_includes,
  dependencies: [lib_logging_dep, lib_balancer_cp_dep, lib_balancer_dp_dep, libdpdk_dep, lib_common_dep,
  lib_logging_dep,
  libdpdk_inc_dep,
  lib_filter_dep,  lib_packet_dp_dep,
  lib_module_dp_dep, lib_config_cp_dep],
)

test(
  'lookup_vs',
  lookup_vs_exe,
  timeout: 30,
  suite: 'balancer',
)
