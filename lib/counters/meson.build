dependencies = []

dependencies += lib_common_dep
#dependencies += lib_module_dp_dep
#dependencies += lib_pipeline_dp_dep

sources = files(
  'counters.c',
)

lib_counters = static_library(
  'counters',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_counters_dep = declare_dependency(
  link_with: lib_counters,
  include_directories: yanet_libdir,
)

