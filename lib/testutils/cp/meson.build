lib_testutils_cp_deps = [
  lib_common_dep,
  lib_logging_dep,
  lib_errors_dep,
  lib_config_cp_dep,
  lib_agent_cp_dep,
  lib_counters_dep,
  lib_pipeline_dp_dep,
]

lib_testutils_cp = static_library(
  'testutils_cp',
  'cp.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args + ['-ldl'],
  dependencies: lib_testutils_cp_deps,
  include_directories: [yanet_rootdir, yanet_libdir],
  install: false,
)

lib_testutils_cp_dep = declare_dependency(
  link_with: lib_testutils_cp,
  dependencies: lib_testutils_cp_deps,
  include_directories: [yanet_rootdir, yanet_libdir],
)

# Modules linked into the smoke test so dlsym(RTLD_DEFAULT) can resolve
# new_module_route and new_module_decap.
smoke_modules_dep = [
  lib_decap_dp_dep,
  lib_dscp_dp_dep,
  lib_acl_dp_dep,
  lib_fwstate_dp_dep,
  lib_forward_dp_dep,
  lib_route_dp_dep,
  lib_nat64_dp_dep,
  lib_pdump_dp_dep,
]

smoke_devices_dep = [
  lib_dev_plain_dp_dep,
  lib_dev_vlan_dp_dep,
]

cp_smoke_test = executable(
  'cp_smoke_test',
  'cp_smoke_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args + ['-ldl'],
  dependencies: [
    lib_testutils_cp_dep,
    libdpdk_inc_dep,
  ] + smoke_modules_dep + smoke_devices_dep,
  include_directories: [yanet_rootdir, yanet_libdir],
)

test('cp_smoke_test', cp_smoke_test, suite: 'testutils')
