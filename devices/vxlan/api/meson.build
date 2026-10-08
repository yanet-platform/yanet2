cp_dependencies = [
  lib_common_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_agent_cp_dep,
]

sources = files(
  'controlplane.c',
)

lib_dev_vxlan_api = static_library(
  'dev_vxlan_api',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: yanet_rootdir,
  install: false,
)

lib_dev_vxlan_api = declare_dependency(
  link_with: lib_dev_vxlan_api,
)
