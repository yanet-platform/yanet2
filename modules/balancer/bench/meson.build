dependencies = [
  lib_common_dep,
  lib_lib_utils_dep,
]

sources = files(
    'alloc.c',
    'bench.c',
)

static_library(
  'balancer_bench',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: [yanet_rootdir],
  install: false,
)