deps = [lib_common_dep, lib_packet_dp_dep, lib_filter_dep, lib_filter_utils_dep, libdpdk_inc_dep, libdpdk_dep]

# bench and compare with previous filter
executable('prev_vs_func',
 ['prev_vs_func.c'],
  c_args: yanet_c_args + ['-O3', '-DIPFW_SKIP_NET6'],
  link_args: yanet_link_args,
  dependencies: deps,
  link_language: 'c'
)

# bench macros and compare with previous filter
executable('prev_vs_macros',
['prev_vs_macros.c'],
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