cancellation_test = executable(
  'cancellation_test',
  ['cancellation_test.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_cancellation_dep,
    lib_logging_dep,
    dependency('threads'),
  ],
  include_directories: yanet_rootdir,
)

test('cancellation', cancellation_test, suite: ['lib'])
