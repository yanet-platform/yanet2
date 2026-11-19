nat64s_test_c_args = []
nat64s_test_dependencies = [
  lib_dataplane_ut_dep,
  lib_logging_dep,
  lib_dev_plain_dp_dep,
  lib_agent_cp_dep,
  libdpdk_test_dep,
]

if not dataplane_only
  nat64s_test_c_args += ['-DYANET_DATAPLANE_UT_CONTROLPLANE']
  nat64s_test_dependencies += [
    lib_dev_plain_api_dep,
    lib_forward_cp_dep,
    lib_forward_dp_dep,
    lib_nat64s_cp_dep,
    lib_nat64s_dp_dep,
    lib_fwstate_objects_dep,
    lib_errors_dep,
  ]
endif

nat64s_test = executable(
  'nat64s_test',
  ['nat64s_test.c'],
  c_args: nat64s_test_c_args,
  dependencies: nat64s_test_dependencies,
  include_directories: yanet_rootdir,
)

test('nat64s', nat64s_test, suite: 'dataplane_ut')
