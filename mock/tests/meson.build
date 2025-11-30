subdir('my_module')

includes = include_directories('../../', '../../lib')

lib_dependencies = [
    lib_common_dep,
    lib_logging_dep,
    lib_config_cp_dep,
    lib_agent_cp_dep,
    lib_counters_dep,
    lib_pipeline_dp_dep,
    libdpdk_inc_dep,
    lib_dev_plain_api,
    lib_dev_vlan_api,
    lib_lib_utils_dep,
    lib_my_module_cp_dep,
]

modules_dep = [
    lib_my_module_dp_dep, # instead of balancer module
    lib_decap_dp_dep,
    lib_dscp_dp_dep,
    lib_acl_dp_dep,
    lib_forward_dp_dep,
    lib_route_dp_dep,
    lib_nat64_dp_dep,
    lib_pdump_dp_dep,
]

devices_dep = [
    lib_dev_plain_dp_dep,
    lib_dev_vlan_dp_dep,
]

deps = modules_dep + devices_dep + lib_dependencies + [lib_yanet_mock_dep]

basic_test = executable('basic_test', ['basic_test.c'], dependencies: deps, include_directories: includes)
test('basic_test', basic_test, suite: 'mock')