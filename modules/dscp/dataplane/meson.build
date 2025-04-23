# common dependecies
dependencies += lib_common_dep

# dataplane dependecies
dp_dependencies = dependencies
dp_dependencies += lib_packet_dp_dep

dp_dependencies += lib_module_dp_dep

dp_sources = files(
  'dataplane.c',
)

lib_dscp_dp = static_library(
  'dscp_dp',
  dp_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dp_dependencies,
  install: false,
)

lib_dscp_dp_dep = declare_dependency(
  link_with: lib_dscp_dp,
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_module_dscp=new_module_dscp',
    '-Wl,--export-dynamic-symbol=new_module_dscp',
  ],
)
