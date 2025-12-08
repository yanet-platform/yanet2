test(
  'go_mock',
  find_program('go'),
  env: yanet_go_env,
  args: ['test'] + (get_option('b_coverage') ? ['-cover', '-args', '-test.gocoverdir=' + meson.current_build_dir()]: []),
  workdir: meson.current_source_dir(),
  timeout: 120,
  depends: lib_yanet_mock,
  suite: 'mock',
)
