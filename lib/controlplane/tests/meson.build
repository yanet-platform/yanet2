counters_uaf_test = executable(
  'counters_uaf_test',
  ['counters_test.c'],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_agent_counters_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)

test('counters_uaf', counters_uaf_test, suite: ['lib'])

counters_lock_thread_dep = dependency('threads')

counters_lock_test = executable(
  'counters_lock_test',
  ['counters_lock_test.c'],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_agent_counters_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_test_dep,
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
  dependencies: [
    lib_dataplane_ut_dep,
    lib_agent_counters_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_counters_dep,
    lib_dev_plain_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)

test('counter_workers', counter_workers_test, suite: ['lib'])

loaded_counts_test = executable(
  'loaded_counts_test',
  ['loaded_counts_test.c'],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)

test('loaded_counts', loaded_counts_test, suite: ['lib'])

gen_pin_race_thread_dep = dependency('threads')

gen_pin_race_test = executable(
  'gen_pin_race_test',
  ['gen_pin_race_test.c'],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_test_dep,
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
    '-Wl,--wrap=malloc',
    '-Wl,--wrap=calloc',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_test_dep,
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
  dependencies: [
    lib_logging_dep,
    lib_config_cp_dep,
  ],
  include_directories: yanet_rootdir,
)

test('module_registry_fini_guard', module_registry_fini_guard_test, suite: ['lib'])

cp_object_test = executable(
  'cp_object_test',
  ['cp_object_test.c'],
  # The object loader looks up the test's own constructor by name in the
  # executable.
  link_args: [
    '-Wl,--export-dynamic-symbol=new_object_test',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_counters_dep,
    lib_dev_plain_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)

test('cp_object', cp_object_test, suite: ['lib'])

cp_object_gen_test = executable(
  'cp_object_gen_test',
  ['cp_object_gen_test.c'],
  # The object loader looks up the test's own constructor by name in the
  # executable.
  link_args: [
    '-Wl,--export-dynamic-symbol=new_object_test',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api_dep,
    lib_forward_cp_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_counters_dep,
    lib_dev_plain_dp_dep,
    lib_forward_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)

test('cp_object_gen', cp_object_gen_test, suite: ['lib'])

prepare_test = executable(
  'prepare_test',
  ['prepare_test.c'],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api_dep,
    lib_forward_cp_dep,
    lib_decap_cp_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dp_zone_dep,
    lib_dev_plain_dp_dep,
    lib_forward_dp_dep,
    lib_decap_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)

test('prepare', prepare_test, suite: ['lib'])

object_prepare_test = executable(
  'object_prepare_test',
  ['object_prepare_test.c'],
  # The object loader looks up the test's own constructors by name in the
  # executable.
  link_args: [
    '-Wl,--export-dynamic-symbol=new_object_hook',
    '-Wl,--export-dynamic-symbol=new_object_inert',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dp_zone_dep,
    lib_dev_plain_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)

test('object_prepare', object_prepare_test, suite: ['lib'])

dangling_modules_test = executable(
  'dangling_modules_test',
  ['dangling_modules_test.c'],
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
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)

test('dangling_modules', dangling_modules_test, suite: ['lib'])

dangling_devices_test = executable(
  'dangling_devices_test',
  ['dangling_devices_test.c'],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api_dep,
    lib_dev_vlan_api_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    lib_dev_vlan_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)

test('dangling_devices', dangling_devices_test, suite: ['lib'])

dangling_objects_test = executable(
  'dangling_objects_test',
  ['dangling_objects_test.c'],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_fwstate_objects_dep,
    lib_agent_cp_dep,
    lib_config_cp_dep,
    lib_dev_plain_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)

test('dangling_objects', dangling_objects_test, suite: ['lib'])
superseded_agent_test = executable(
  'superseded_agent_test',
  ['superseded_agent_test.c'],
  link_args: [
    '-Wl,-E',
    '-Wl,--undefined=new_device_plain',
    '-Wl,--export-dynamic-symbol=new_device_plain',
    '-Wl,--undefined=new_module_decap',
    '-Wl,--export-dynamic-symbol=new_module_decap',
  ],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_dev_plain_api,
    lib_decap_cp_dep,
    lib_fwstate_objects_dep,
    libdpdk_dep,
  ],
  link_with: [lib_dev_plain_dp, lib_decap_dp],
  include_directories: yanet_rootdir,
)

test('superseded_agent', superseded_agent_test, suite: ['lib'])

agent_list_race_thread_dep = dependency('threads')

agent_list_race_test = executable(
  'agent_list_race_test',
  ['agent_list_race_test.c'],
  link_args: [
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
    agent_list_race_thread_dep,
  ],
  link_with: [lib_dev_plain_dp],
  include_directories: yanet_rootdir,
)

test(
  'agent_list_race',
  agent_list_race_test,
  suite: ['lib'],
  timeout: 90,
)
