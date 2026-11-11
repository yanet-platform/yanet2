vlan_leak_test = executable(
  'vlan_leak_test',
  'leak_test.c',
  dependencies: [
    lib_dev_vlan_api_dep,
    lib_common_dep,
    lib_logging_dep,
  ],
  include_directories: yanet_rootdir,
)
test('vlan_leak', vlan_leak_test, suite: 'devices')

vlan_output_test = executable(
  'vlan_output_test',
  'output_test.c',
  dependencies: [
    lib_dataplane_ut_dep,
    lib_dev_vlan_api_dep,
    lib_dev_vlan_dp_dep,
    lib_lib_utils_dep,
    lib_agent_cp_dep,
    lib_dp_zone_dep,
    lib_logging_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)
test('vlan_output', vlan_output_test, suite: 'devices')
