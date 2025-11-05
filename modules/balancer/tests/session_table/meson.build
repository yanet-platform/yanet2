state_test_sources = files(
  'worker.c',
  'helpers.c',
  'controlplane.c',
  'main.c',
  'run.c'
)

state_test_includes = [
  yanet_rootdir,
  '../..',
]

session_table_test_exe = executable(
  'session_table',
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  sources: state_test_sources,
  include_directories: state_test_includes,
  dependencies: [lib_logging_dep, lib_balancer_test_utils_dep, libdpdk_dep],
)

test(
  'session_table',
  session_table_test_exe,
  timeout: 30,
  suite: ['balancer'],
)