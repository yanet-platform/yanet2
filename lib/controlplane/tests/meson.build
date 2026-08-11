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

counters_lock_thread_dep = dependency('threads')

counters_lock_test = executable(
  'counters_lock_test',
  ['counters_lock_test.c'],
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
    counters_lock_thread_dep,
  ],
  link_with: [lib_dev_plain_dp],
  include_directories: yanet_rootdir,
)

test(
  'counters_lock',
  counters_lock_test,
  suite: ['lib'],
  timeout: 90,
)

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
    '-Wl,--undefined=new_module_forward',
    '-Wl,--export-dynamic-symbol=new_module_forward',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_forward_cp_dep,
    libdpdk_dep,
  ],
  link_with: [lib_dev_plain_dp, lib_forward_dp],
  include_directories: yanet_rootdir,
)

test('cp_object_gen', cp_object_gen_test, suite: ['lib'])
