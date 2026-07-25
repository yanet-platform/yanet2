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

loaded_counts_test = executable(
  'loaded_counts_test',
  ['loaded_counts_test.c'],
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

test('loaded_counts', loaded_counts_test, suite: ['lib'])

cp_object_test = executable(
  'cp_object_test',
  ['cp_object_test.c'],
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

test('cp_object', cp_object_test, suite: ['lib'])

cp_object_gen_test = executable(
  'cp_object_gen_test',
  ['cp_object_gen_test.c'],
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

test('cp_object_gen', cp_object_gen_test, suite: ['lib'])
