vxlan_leak_test = executable(
  'vxlan_leak_test',
  'leak_test.c',
  dependencies: [
    lib_dev_vxlan_api_dep,
    lib_common_dep,
    lib_logging_dep,
  ],
  include_directories: yanet_rootdir,
)
test('vxlan_leak', vxlan_leak_test, suite: 'devices')

vxlan_dataplane_test = executable(
  'vxlan_dataplane_test',
  'dataplane_test.c',
  dependencies: [
    lib_dataplane_ut_dep,
    lib_dev_vxlan_api_dep,
    lib_lib_utils_dep,
    lib_packet_dp_dep,
    lib_logging_dep,
    lib_agent_cp_dep,
    lib_dp_zone_dep,
    lib_dev_vxlan_dp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)
test('vxlan_dataplane', vxlan_dataplane_test, suite: 'devices')
