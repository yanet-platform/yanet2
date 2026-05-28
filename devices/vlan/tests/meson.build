vlan_leak_test = executable(
  'vlan_leak_test',
  'leak_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_dev_vlan_api,
    lib_config_cp_dep,
    lib_agent_cp_dep,
    lib_common_dep,
    lib_logging_dep,
    lib_errors_dep,
  ],
  include_directories: yanet_rootdir,
)
test('vlan_leak', vlan_leak_test, suite: 'devices')
