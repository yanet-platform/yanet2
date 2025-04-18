sources = files(
  'decap.go',
  'decap_test.go',
)

test(
  'decap',
  find_program('go'),
  args: ['test', './...'] + (get_option('b_coverage') ? ['-cover', '-args', '-test.gocoverdir=' + meson.current_build_dir()]: []),
  workdir: meson.current_source_dir(),
  timeout: 120,
  env: test_env,
)
