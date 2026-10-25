dependencies = [
  lib_counters_dep,
  lib_dp_zone_dep,
  lib_errors_dep,
]

sources = files(
  'cp_module.c',
  'cp_chain.c',
  'cp_function.c',
  'cp_pipeline.c',
  'cp_device.c',
  'cp_object.c',
  'cp_counter.c',
  'econtext.c',
  'reclaim.c',
  'zone.c',
)

# The registry debug invariants rescan the whole registry after every
# mutation, so only debug and sanitized builds enable them; release
# mutations stay constant time.
registry_sanitize_args = []
if get_option('buildtype') == 'debug' or get_option('b_sanitize') != 'none'
  registry_sanitize_args += '-DREGISTRY_SANITIZE'
endif

lib_config_cp = static_library(
  'config_cp',
  sources,
  c_args: yanet_c_args + registry_sanitize_args,
  dependencies: dependencies,
  install: false,
)

lib_config_cp_dep = declare_dependency(
  link_with: lib_config_cp,
  dependencies : [lib_counters_dep, lib_errors_dep, lib_dp_zone_dep],
  include_directories: yanet_rootdir,
)
