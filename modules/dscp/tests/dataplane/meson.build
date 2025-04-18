sources = files(
  'dscp.go',
  'dscp_test.go',
)

test(
  'dscp',
  find_program('go'),
  args: ['test', './...'] + (get_option('b_coverage') ? ['-cover', '-args', '-test.gocoverdir=' + meson.current_build_dir()]: []),
  workdir: meson.current_source_dir(),
  timeout: 120,
  env: test_env,
)
