vxlan_leak_test = executable(
  'vxlan_leak_test',
  'leak_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_dev_vxlan_api_dep,
    lib_config_cp_dep,
    lib_agent_cp_dep,
    lib_common_dep,
    lib_logging_dep,
    lib_errors_dep,
  ],
  include_directories: yanet_rootdir,
)
test('vxlan_leak', vxlan_leak_test, suite: 'devices')

vxlan_dataplane_test = executable(
  'vxlan_dataplane_test',
  'dataplane_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args + [
    '-Wl,-E',
    '-Wl,--undefined=new_device_plain',
    '-Wl,--export-dynamic-symbol=new_device_plain',
    '-Wl,--undefined=new_device_vxlan',
    '-Wl,--export-dynamic-symbol=new_device_vxlan',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_dev_vxlan_api_dep,
    lib_lib_utils_dep,
    lib_packet_dp_dep,
    lib_logging_dep,
    lib_errors_dep,
    libdpdk_dep,
  ],
  link_with: [lib_dev_vxlan_dp, lib_dev_plain_dp],
  include_directories: yanet_rootdir,
)
test('vxlan_dataplane', vxlan_dataplane_test, suite: 'devices')
