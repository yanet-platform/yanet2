test_env = environment(
  {
    'MALLOC_PERTURB_': '0',
    'ASAN_OPTIONS': '',
    'UBSAN_OPTIONS': '',
    'MSAN_OPTIONS': '',
  },
)

test(
  'balancer',
  go,
  args: ['test'] + (get_option('b_coverage') ? ['-cover', '-args', '-test.gocoverdir=' + meson.current_build_dir()]: []),
  workdir: meson.current_source_dir(),
  timeout: 120,
  env: test_env,
  depends: [
    lib_testing_module_dp, lib_balancer_test_utils
  ],
  suite: 'balancer',
)
