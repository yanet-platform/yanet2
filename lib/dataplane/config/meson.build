dependencies = [
  # The plugin loader opens shared objects and the loaders look up
  # constructors in them.
  dependency('dl'),
  lib_dp_zone_dep,
  lib_errors_dep,
  lib_logging_dep,
  lib_counters_dep,
  lib_config_cp_dep,
]

sources = files(
  'agent.c',
  'bootstrap.c',
  'counter_storage.c',
  'module_loader.c',
  'object_loader.c',
  'plugin_loader.c',
  'topology.c',
)

lib_config_dp = static_library(
  'config_dp',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_config_dp_dep = declare_dependency(
  link_with: [lib_config_dp],
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)

# The ABI version a plugin .so was built against, which the loader checks
# before it accepts the plugin; the loader test's plugin takes it.
#
# Each plugin takes it exactly once, as a source or through the dependency
# below. The built-in modules never link it, so the dataplane binary defines
# no version symbol of its own.
lib_module_abi_export_sources = files('plugin_abi_export.c')

lib_module_abi_export_dep = declare_dependency(
  sources: lib_module_abi_export_sources,
  include_directories: yanet_rootdir,
)
