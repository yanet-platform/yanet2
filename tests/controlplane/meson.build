config_gen_test = executable(
  'config_gen_test',
  ['config_gen_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_common_dep,
    lib_logging_dep,
    lib_config_cp_dep,
    lib_config_dp_dep,
  ],
  include_directories: yanet_libdir,
)
test('config_gen', config_gen_test, suite: ['controlplane'])
