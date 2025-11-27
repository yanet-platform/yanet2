cp_dependencies = [
  lib_common_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_agent_cp_dep,
]

includes = include_directories('../dataplane')

sources = files(
  'controlplane.c',
)

lib_dev_vlan_api = static_library(
  'dev_vlan_api',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_dev_vlan_api = declare_dependency(
  link_with: lib_dev_vlan_api,
)
