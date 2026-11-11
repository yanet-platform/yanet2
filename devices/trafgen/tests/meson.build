trafgen_leak_test = executable(
  'trafgen_leak_test',
  'leak_test.c',
  dependencies: [
    lib_dev_trafgen_api_dep,
    lib_common_dep,
    lib_logging_dep,
  ],
  include_directories: yanet_rootdir,
)
test('trafgen_leak', trafgen_leak_test, suite: 'devices')
