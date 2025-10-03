state_test_sources = files(
  'main.c',
)

state_test_includes = [
  yanet_rootdir,
  '../..',
]

state_test_exe = executable(
  'ring_test',
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  sources: state_test_sources,
  include_directories: state_test_includes,
  dependencies: [lib_logging_dep, lib_balancer_dp_dep],
)

test(
  'state',
  state_test_exe,
  timeout: 30,
  suite: 'balancer',
)