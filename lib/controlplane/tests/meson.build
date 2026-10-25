counters_test = executable(
  'counters_test',
  ['counters_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_agent_counters_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('counters_uaf', counters_test, suite: ['lib'])

counters_lock_thread_dep = dependency('threads')

counters_lock_test = executable(
  'counters_lock_test',
  ['counters_lock_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_agent_counters_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_dep,
    counters_lock_thread_dep,
  ],
  include_directories: yanet_rootdir,
)

test(
  'counters_lock',
  counters_lock_test,
  suite: ['lib'],
  timeout: 90,
)

counter_workers_test = executable(
  'counter_workers_test',
  ['counter_workers_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_agent_counters_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_counters_dep,
    lib_dev_plain_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('counter_workers', counter_workers_test, suite: ['lib'])

loaded_counts_test = executable(
  'loaded_counts_test',
  ['loaded_counts_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('loaded_counts', loaded_counts_test, suite: ['lib'])

gen_pin_race_thread_dep = dependency('threads')

gen_pin_race_test = executable(
  'gen_pin_race_test',
  ['gen_pin_race_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_dep,
    gen_pin_race_thread_dep,
  ],
  include_directories: yanet_rootdir,
)

test(
  'gen_pin_race',
  gen_pin_race_test,
  suite: ['lib'],
  timeout: 90,
)

device_list_thread_dep = dependency('threads')

device_list_test = executable(
  'device_list_test',
  ['device_list_test.c'],
  link_args: [
    '-Wl,-E',
    '-Wl,--wrap=malloc',
    '-Wl,--wrap=calloc',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_dep,
    device_list_thread_dep,
  ],
  include_directories: yanet_rootdir,
)

test(
  'device_list',
  device_list_test,
  suite: ['lib'],
  timeout: 30,
)

module_registry_fini_guard_test = executable(
  'module_registry_fini_guard_test',
  ['module_registry_fini_guard_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_logging_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('module_registry_fini_guard', module_registry_fini_guard_test, suite: ['lib'])

cp_object_test = executable(
  'cp_object_test',
  ['cp_object_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_counters_dep,
    lib_dev_plain_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('cp_object', cp_object_test, suite: ['lib'])

cp_object_gen_test = executable(
  'cp_object_gen_test',
  ['cp_object_gen_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_forward_cp_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_counters_dep,
    lib_dev_plain_dp_dep,
    lib_forward_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('cp_object_gen', cp_object_gen_test, suite: ['lib'])

prepare_test = executable(
  'prepare_test',
  ['prepare_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_forward_cp_dep,
    lib_decap_cp_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dp_zone_dep,
    lib_dev_plain_dp_dep,
    lib_forward_dp_dep,
    lib_decap_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('prepare', prepare_test, suite: ['lib'])

object_prepare_test = executable(
  'object_prepare_test',
  ['object_prepare_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dp_zone_dep,
    lib_dev_plain_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('object_prepare', object_prepare_test, suite: ['lib'])

dangling_modules_test = executable(
  'dangling_modules_test',
  ['dangling_modules_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_decap_cp_dep,
    lib_forward_cp_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_decap_dp_dep,
    lib_forward_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('dangling_modules', dangling_modules_test, suite: ['lib'])

dangling_devices_test = executable(
  'dangling_devices_test',
  ['dangling_devices_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_dev_vlan_api,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    lib_dev_vlan_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('dangling_devices', dangling_devices_test, suite: ['lib'])

dangling_objects_test = executable(
  'dangling_objects_test',
  ['dangling_objects_test.c'],
  link_args: [
    '-Wl,-E',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_fwstate_objects_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('dangling_objects', dangling_objects_test, suite: ['lib'])