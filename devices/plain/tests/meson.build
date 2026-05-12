plain_leak_test = executable(
  'plain_leak_test',
  'leak_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_dev_plain_api,
    lib_config_cp_dep,
    lib_agent_cp_dep,
    lib_common_dep,
    lib_logging_dep,
    lib_errors_dep,
  ],
  include_directories: yanet_rootdir,
)
test('plain_leak', plain_leak_test, suite: 'devices')
