counters_test = executable(
  'counters_test',
  ['counters_test.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args + [
    '-Wl,-E',
    '-Wl,--undefined=new_device_plain',
    '-Wl,--export-dynamic-symbol=new_device_plain',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    libdpdk_dep,
  ],
  link_with: [lib_dev_plain_dp],
  include_directories: yanet_rootdir,
)

test('counters_uaf', counters_test, suite: ['lib'])

unused_park_test = executable(
  'unused_park_test',
  ['unused_park_test.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args + [
    '-Wl,-E',
    '-Wl,--undefined=new_device_plain',
    '-Wl,--export-dynamic-symbol=new_device_plain',
    '-Wl,--undefined=new_module_route',
    '-Wl,--export-dynamic-symbol=new_module_route',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_route_cp_dep,
    libdpdk_dep,
  ],
  link_with: [lib_dev_plain_dp, lib_route_dp],
  include_directories: yanet_rootdir,
)

test('unused_park', unused_park_test, suite: ['lib'])
