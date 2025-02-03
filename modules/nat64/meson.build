# common dependecies
dependencies += lib_common_dep
dependencies += lib_filter_dep

# dataplane dependecies
dp_dependencies = dependencies
dp_dependencies += lib_packet_dp_dep

dp_dependencies += lib_module_dp_dep

dp_sources = files(
  'dataplane.c',
)

lib_nat64_dp = static_library(
  'nat64_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_nat64_dp_dep = declare_dependency(
  link_with: lib_nat64_dp,
  link_args: [
    '-Wl,--defsym', '-Wl,new_module_nat64=new_module_nat64',
    '-Wl,--export-dynamic-symbol=new_module_nat64',
  ]
)


subdir('unit')