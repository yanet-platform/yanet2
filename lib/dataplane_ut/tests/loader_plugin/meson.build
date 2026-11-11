# The loader test's plugin: constructors for the loader_probe object type.
#
# Beside the ABI version every plugin exports, it carries a probe constructor
# and an alias constructor that returns a mismatched type name.
loader_probe_plugin = shared_module(
  'loader_probe_dp',
  ['loader_plugin.c'] + lib_module_abi_export_sources,
  name_prefix: 'lib',
  c_args: ['-fvisibility=hidden'],
  include_directories: yanet_rootdir,
  install: false,
)

loader_probe_plugin_dir = meson.current_build_dir()
loader_probe_include = include_directories('.')
