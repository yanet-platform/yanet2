deps = [lib_common_dep, lib_packet_dp_dep, lib_filter_dep, lib_filter_utils_dep, libdpdk_inc_dep, libdpdk_dep]

# bench macros and compare with previous filter
executable('func_vs_macros',
['func_vs_macros.c'],
  c_args: yanet_c_args + ['-O3', '-DIPFW_SKIP_NET6'],
  link_args: yanet_link_args,
  dependencies: deps,
  link_language: 'c'
)

# bench macros
executable('macros',
 ['macros.c'],
  c_args: yanet_c_args + ['-O3'],
  link_args: yanet_link_args,
  dependencies: deps,
  link_language: 'c'
)
