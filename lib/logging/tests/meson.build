log_rate_test = executable(
  'log_rate_test',
  ['rate.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_logging_dep, dependency('threads')],
  include_directories: yanet_rootdir,
)

test('log_rate', log_rate_test, suite: ['logging'])
