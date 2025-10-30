dp_dependencies = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_module_dp_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_dev_vlan_dp = static_library(
  'vlan_dp',
  dp_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dp_dependencies,
  install: false,
)

lib_dev_vlan_dp_dep = declare_dependency(
  link_with: lib_dev_vlan_dp,
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_device_vlan=new_device_vlan',
    '-Wl,--export-dynamic-symbol=new_device_vlan',
  ],
)
