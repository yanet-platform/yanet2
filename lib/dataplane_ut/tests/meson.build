smoke_test_c_args = yanet_c_args
smoke_test_dependencies = [
  lib_dataplane_ut_dep,
  lib_logging_dep,
  libdpdk_dep,
]
smoke_test_link_with = [lib_dev_plain_dp]
smoke_test_link_args = yanet_link_args + [
  '-Wl,-E',
  '-Wl,--undefined=new_device_plain',
  '-Wl,--export-dynamic-symbol=new_device_plain',
]

if not dataplane_only
  smoke_test_c_args += ['-DYANET_DATAPLANE_UT_CONTROLPLANE']
  smoke_test_dependencies += [
    lib_dev_plain_api,
    lib_decap_cp_dep,
    lib_dscp_cp_dep,
    lib_forward_cp_dep,
  ]
  smoke_test_link_with += [lib_decap_dp, lib_dscp_dp, lib_forward_dp]
  smoke_test_link_args += [
    '-Wl,--undefined=new_module_decap',
    '-Wl,--export-dynamic-symbol=new_module_decap',
    '-Wl,--undefined=new_module_dscp',
    '-Wl,--export-dynamic-symbol=new_module_dscp',
    '-Wl,--undefined=new_module_forward',
    '-Wl,--export-dynamic-symbol=new_module_forward',
  ]
endif

smoke_test = executable(
  'dataplane_ut_smoke_test',
  ['smoke_test.c'],
  c_args: smoke_test_c_args,
  link_args: smoke_test_link_args,
  dependencies: smoke_test_dependencies,
  link_with: smoke_test_link_with,
  include_directories: yanet_rootdir,
)
test('dataplane_ut_smoke', smoke_test, suite: 'dataplane_ut')

worker_counters_test = executable(
  'worker_counters_test',
  ['worker_counters_test.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)
test('worker_counters', worker_counters_test, suite: 'dataplane_ut')
