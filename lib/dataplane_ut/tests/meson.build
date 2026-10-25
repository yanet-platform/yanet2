smoke_test_c_args = []
smoke_test_dependencies = [
  lib_dataplane_ut_dep,
  lib_logging_dep,
  lib_dev_plain_dp_dep,
  lib_agent_cp_dep,
  libdpdk_test_dep,
]
smoke_test_link_args = [
  '-Wl,-E',
]

if not dataplane_only
  smoke_test_c_args += ['-DYANET_DATAPLANE_UT_CONTROLPLANE']
  smoke_test_dependencies += [
    lib_dev_plain_api,
    lib_decap_cp_dep,
    lib_dscp_cp_dep,
    lib_forward_cp_dep,
    lib_decap_dp_dep,
    lib_dscp_dp_dep,
    lib_forward_dp_dep,
    lib_errors_dep,
    lib_packet_dp_dep,
  ]
endif

smoke_test = executable(
  'dataplane_ut_smoke_test',
  ['smoke_test.c'],
  c_args: smoke_test_c_args,
  link_args: smoke_test_link_args,
  dependencies: smoke_test_dependencies,
  include_directories: yanet_rootdir,
)
test('dataplane_ut_smoke', smoke_test, suite: 'dataplane_ut')

worker_counters_test = executable(
  'worker_counters_test',
  ['worker_counters_test.c'],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_agent_cp_dep,
    lib_worker_dp_dep,
    libdpdk_test_dep,
  ],
  include_directories: yanet_rootdir,
)
test('worker_counters', worker_counters_test, suite: 'dataplane_ut')

subdir('loader_plugin')

loader_test = executable(
  'loader_test',
  ['loader_test.c'],
  c_args: [
    '-DLOADER_PLUGIN_DIR="@0@"'.format(loader_probe_plugin_dir),
  ],
  link_args: ['-Wl,-E'],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_agent_cp_dep,
    lib_config_dp_dep,
    lib_dp_zone_dep,
    libdpdk_test_dep,
  ],
  include_directories: [yanet_rootdir, loader_probe_include],
)
test(
  'loader_plugin_types',
  loader_test,
  depends: loader_probe_plugin,
  suite: 'dataplane_ut',
)
