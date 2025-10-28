test_includes = [
  yanet_rootdir,
  '../..',
]

# ////////////////////////////////////////////////////////////////////////////////

lookup_vs_test_sources = files(
  'lookup_vs.c',
)

lookup_vs_exe = executable(
  'lookup_vs',
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  sources: lookup_vs_test_sources,
  include_directories: test_includes,
  dependencies: [
    lib_logging_dep,
    lib_balancer_cp_dep, 
    lib_balancer_dp_dep,
    libdpdk_dep, 
    lib_config_dp_dep,
    lib_balancer_test_utils_dep
  ],
)

test(
  'lookup_vs',
  lookup_vs_exe,
  timeout: 30,
  suite: 'balancer',
)

# ////////////////////////////////////////////////////////////////////////////////

select_real_test_sources = files(
  'select_real.c',
)

select_real = executable(
  'select_real',
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  sources: select_real_test_sources,
  include_directories: test_includes,
  dependencies: [
    lib_logging_dep,
    lib_balancer_cp_dep, 
    lib_balancer_dp_dep,
    libdpdk_dep, 
    lib_config_dp_dep,
    lib_balancer_test_utils_dep
  ],
)

test('select_real', select_real, timeout: 30, suite: 'balancer')

# # ////////////////////////////////////////////////////////////////////////////////

metadata_test_sources = files(
  'metadata.c',
)

metadata = executable(
  'metadata',
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  sources: metadata_test_sources,
  include_directories: test_includes,
  dependencies: [
    lib_logging_dep,
    lib_balancer_cp_dep, 
    lib_balancer_dp_dep,
    libdpdk_dep, 
    lib_config_dp_dep,
    lib_balancer_test_utils_dep
  ],
)

test('metadata', metadata, timeout: 5, suite: 'balancer')

# # ////////////////////////////////////////////////////////////////////////////////

tunnel_test_sources = files(
  'tunnel.c',
)

tunnel = executable(
  'tunnel',
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  sources: tunnel_test_sources,
  include_directories: test_includes,
  dependencies: [
    lib_logging_dep,
    lib_balancer_cp_dep, 
    lib_balancer_dp_dep,
    libdpdk_dep, 
    lib_config_dp_dep,
    lib_balancer_test_utils_dep
  ],
)

test('tunnel', tunnel, timeout: 30, suite: 'balancer')