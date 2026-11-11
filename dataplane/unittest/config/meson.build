dp_config_test = executable(
  'dataplane-config-test',
  'main.c',
  c_args: yanet_test_c_args + [
    '-DCONFIG_PATH="@0@"'.format(meson.current_source_dir() / 'dataplane.yaml'),
  ],
  include_directories: yanet_rootdir,
  dependencies: lib_dataplane_dep,
  link_args: ['-Wl,--wrap=realloc'],
)
test('dataplane_config', dp_config_test)
