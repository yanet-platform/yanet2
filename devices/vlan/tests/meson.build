vlan_leak_test = executable(
  'vlan_leak_test',
  'leak_test.c',
  dependencies: [
    lib_dev_vlan_api,
    lib_common_dep,
    lib_logging_dep,
  ],
  include_directories: yanet_rootdir,
)
test('vlan_leak', vlan_leak_test, suite: 'devices')
