# Single source of truth for this plugin's TUs: feeds both dp_sources and
# dp_source_paths below.
dp_source_names = ['good.c']
dp_sources = files(dp_source_names)

dp_source_paths = []
foreach f : dp_source_names
  dp_source_paths += join_paths(meson.current_source_dir(), f)
endforeach

good_abi_manifest_c_path = join_paths(meson.current_build_dir(), 'good_abi_manifest.c')
good_abi_manifest_dep_path = join_paths(meson.current_build_dir(), 'good_abi_manifest.d')

good_abi_ct = custom_target(
  'good_abi_manifest',
  output: ['good_abi_manifest.c'],
  command: abigen_run + [
    '-compile-commands', abi_compile_commands,
    '-repo-root', meson.project_source_root(),
    '-plugin',
    '-symbol-prefix', 'yanet_module_abi_',
    '-emit-c', good_abi_manifest_c_path,
    '-depfile', good_abi_manifest_dep_path,
  ] + dp_source_paths,
  depfile: 'good_abi_manifest.d',
  build_by_default: false,
)

lib_test_plugin_good = shared_module(
  'abihashtest_dp',
  dp_sources,
  good_abi_ct[0],
  c_args: yanet_c_args + ['-fvisibility=hidden'],
  link_args: yanet_link_args,
  dependencies: [lib_module_dp_dep],
  install: false,
  build_by_default: false,
)

abi_test_good_plugin_dir = meson.current_build_dir()
