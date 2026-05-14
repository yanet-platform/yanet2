includes = include_directories('../../../', '../../../lib')

lib_dependencies = [
    lib_common_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_config_cp_dep,
    lib_agent_cp_dep,
    lib_counters_dep,
    lib_pipeline_dp_dep,
    libdpdk_inc_dep,
    lib_route_cp_dep,
    lib_lib_utils_dep,
    lib_yanet_mock_dep,
]

modules_dep = [
    lib_decap_dp_dep,
    lib_dscp_dp_dep,
    lib_acl_dp_dep,
    lib_fwstate_dp_dep,
    lib_forward_dp_dep,
    lib_route_dp_dep,
    lib_nat64_dp_dep,
    lib_pdump_dp_dep,
]

devices_dep = [
    lib_dev_plain_dp_dep,
    lib_dev_vlan_dp_dep,
]

deps = modules_dep + devices_dep + lib_dependencies

route_api_test = executable(
  'route_api_test',
  ['route_api_test.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: deps,
  include_directories: includes,
)

test('route_api_test', route_api_test, suite: 'route')
