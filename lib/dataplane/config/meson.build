dependencies = [
  lib_module_dp_dep,
  lib_pipeline_dp_dep,
  lib_errors_dep,
  lib_logging_dep,
  lib_counters_dep,
]

sources = files(
  'agent.c',
  'bootstrap.c',
  'counter_storage.c',
  'module_loader.c',
  'plugin_loader.c',
  'topology.c',
  'zone.c',
)

lib_config_dp = static_library(
  'config_dp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_config_dp_dep = declare_dependency(
  link_with: [lib_config_dp],
  include_directories: yanet_libdir,
  dependencies: dependencies,
)

# Source that exports a module .so's compiled-against ABI version, for a
# future module shared_module() target to pull in. Deliberately kept out
# of lib_config_dp: linking it into every statically-linked built-in
# module would make the final dataplane binary define
# yanet_module_abi_version more than once. A shared_module() target picks
# this up by adding lib_module_abi_export_sources to its own sources (or
# lib_module_abi_export_dep to its dependencies) exactly once per .so.
lib_module_abi_export_sources = files('plugin_abi_export.c')

lib_module_abi_export_dep = declare_dependency(
  sources: lib_module_abi_export_sources,
  include_directories: [
    yanet_libdir,
    yanet_rootdir,
  ],
)
