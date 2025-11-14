test(
  'balancer',
  find_program('go'),
  args: ['test'] + (get_option('b_coverage') ? ['-cover', '-args', '-test.gocoverdir=' + meson.current_build_dir()]: []),
  workdir: meson.current_source_dir(),
  timeout: 30,
  depends: [
    lib_testing_module_dp, lib_yanet_test_utils
  ],
  suite: 'balancer',
)
