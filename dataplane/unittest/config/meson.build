sources = files(
  'main.c',
  '../../config.c'
)

project_src_dir = meson.current_source_dir()

dp_config_test = executable(
  'dataplane-config-test',
  sources,
  include_directories: [yanet_rootdir, '.', '../..'],
  dependencies: dependencies,
  c_args: '-DCONFIG_PATH="@0@/dataplane.yaml"'.format(project_src_dir),
)
test('dataplane config', dp_config_test)
