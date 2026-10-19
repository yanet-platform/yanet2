# The plugin of the loader test: an object type exported as
# new_object_loader_probe, beside the ABI version every plugin exports.
loader_probe_plugin = shared_module(
  'loader_probe_dp',
  ['loader_plugin.c'] + lib_module_abi_export_sources,
  name_prefix: 'lib',
  c_args: yanet_c_args + ['-fvisibility=hidden'],
  include_directories: yanet_rootdir,
  install: false,
)

loader_probe_plugin_dir = meson.current_build_dir()
loader_probe_include = include_directories('.')
