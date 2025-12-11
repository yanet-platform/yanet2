dependencies = [
  lib_counters_dep,
  lib_module_dp_dep,
  lib_pipeline_dp_dep,
  lib_diag_dep,
]

sources = files(
  'cp_module.c',
  'cp_chain.c',
  'cp_function.c',
  'cp_pipeline.c',
  'cp_device.c',
  'cp_counter.c',
  'econtext.c',
  'zone.c',
)

lib_config_cp = static_library(
  'config_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_config_cp_dep = declare_dependency(
  link_with: [lib_config_cp],
  dependencies : lib_counters_dep,
  include_directories: yanet_libdir,
)
