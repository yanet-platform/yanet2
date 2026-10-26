plain_leak_test = executable(
  'plain_leak_test',
  'leak_test.c',
  dependencies: [
    lib_dev_plain_api_dep,
    lib_common_dep,
    lib_logging_dep,
  ],
  include_directories: yanet_rootdir,
)
test('plain_leak', plain_leak_test, suite: 'devices')
