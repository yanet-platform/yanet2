route_api_test = executable(
  'route_api_test',
  ['route_api_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_route_cp_dep,
    lib_route_dp_dep,
    lib_dev_plain_dp_dep,
    lib_agent_cp_dep,
    libdpdk_dep,
  ],
)

test('route_api_test', route_api_test, suite: 'route')

route_fib_object_test = executable(
  'route_fib_object_test',
  ['route_fib_object_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_route_cp_dep,
    lib_route_dp_dep,
    lib_dev_plain_dp_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    libdpdk_dep,
  ],
)

test('route_fib_object_test', route_fib_object_test, suite: 'route')
