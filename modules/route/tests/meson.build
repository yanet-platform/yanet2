includes = include_directories('../../../', '../../../lib')

route_api_test = executable(
  'route_api_test',
  ['route_api_test.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args + [
    '-Wl,-E',
    '-Wl,--undefined=new_module_route',
    '-Wl,--export-dynamic-symbol=new_module_route',
    '-Wl,--undefined=new_device_plain',
    '-Wl,--export-dynamic-symbol=new_device_plain',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_route_cp_dep,
    libdpdk_dep,
  ],
  link_with: [lib_route_dp, lib_dev_plain_dp],
  include_directories: includes,
)

test('route_api_test', route_api_test, suite: 'route')
