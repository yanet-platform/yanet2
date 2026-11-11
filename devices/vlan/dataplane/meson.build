dp_dependencies = [
  lib_common_dep,
  libdpdk_inc_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_dev_vlan_dp = static_library(
  'vlan_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_dev_vlan_dp_dep = declare_dependency(
  link_with: lib_dev_vlan_dp,
  link_args: [
    '-Wl,--require-defined=new_device_vlan',
    '-Wl,--export-dynamic-symbol=new_device_vlan',
  ],
  dependencies: dp_dependencies,
)

dataplane_device_deps += lib_dev_vlan_dp_dep
