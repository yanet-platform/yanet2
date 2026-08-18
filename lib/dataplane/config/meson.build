abigen_pkg = './lib/dataplane/config/abi/cmd/abigen'

# `go run`, not a built abigen binary: nothing with a churning mtime to track.
abigen_run = [go, 'run', '-C', meson.project_source_root(), abigen_pkg]

# `-C` above moves abigen's cwd to repo root, so its own paths must be absolute.
abi_manifest_dataplane_c_path = join_paths(meson.current_build_dir(), 'abi_manifest_dataplane.c')
abi_manifest_dataplane_txt_path = join_paths(meson.current_build_dir(), 'abi_manifest.txt')
abi_manifest_dataplane_dep_path = join_paths(meson.current_build_dir(), 'abi_manifest_dataplane.d')

# global_build_root, not project_build_root: meson only ever writes
# compile_commands.json at the outermost build directory.
#
# When this tree is consumed as a subproject, project_build_root() resolves
# to a subprojects/<name>/ directory that never gets a compile_commands.json
# of its own.
abi_compile_commands = join_paths(meson.global_build_root(), 'compile_commands.json')

# The dataplane binary hashes its own whole public surface, under a symbol
# prefix distinct from a plugin's so RTLD_GLOBAL can never resolve a
# plugin's dlsym to the binary's own table.
abi_manifest_dataplane_ct = custom_target(
  'abi_manifest_dataplane',
  output: [
    'abi_manifest_dataplane.c',
    'abi_manifest.txt',
  ],
  command: abigen_run + [
    '-compile-commands', abi_compile_commands,
    '-repo-root', meson.project_source_root(),
    '-symbol-prefix', 'yanet_dataplane_abi_',
    '-emit-c', abi_manifest_dataplane_c_path,
    '-o', abi_manifest_dataplane_txt_path,
    '-depfile', abi_manifest_dataplane_dep_path,
  ],
  depfile: 'abi_manifest_dataplane.d',
  build_by_default: true,
)

abi_manifest_dataplane_c = abi_manifest_dataplane_ct[0]

# A plugin's own ABI table is generated from its own translation units in
# "-plugin" mode, not shared: see lib/tests/dataplane/abi_plugin/good for
# the pattern a module .so target follows to build and export one.

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
  'object_loader.c',
  'plugin_loader.c',
  'topology.c',
  'zone.c',
)

lib_config_dp = static_library(
  'config_dp',
  sources,
  abi_manifest_dataplane_c,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_config_dp_dep = declare_dependency(
  link_with: [lib_config_dp],
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)
