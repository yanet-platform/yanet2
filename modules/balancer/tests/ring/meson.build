test_sources = files(
  'ring.c',
)

ring_test_includes = [
  yanet_rootdir,
  '../..',
]

ring_test_exe = executable(
  'ring',
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  sources: test_sources,
  include_directories: ring_test_includes,
  dependencies: [lib_logging_dep],
)

test(
  'ring',
  ring_test_exe,
  timeout: 10,
  suite: ['balancer'],
)
