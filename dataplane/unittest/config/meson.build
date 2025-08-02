sources = files(
  'main.c',
  '../../config.c'
)

project_src_dir = meson.current_source_dir()

dp_config_test = executable(
  'dataplane-config-test',
  sources: sources,
  c_args: yanet_test_c_args + ['-DCONFIG_PATH="@0@/dataplane.yaml"'.format(project_src_dir)],
  link_args: yanet_link_args,
  include_directories: [yanet_rootdir, '.', '../..'],
  dependencies: dependencies,
)
test('dataplane config', dp_config_test)
