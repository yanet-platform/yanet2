sources = files(
  'nat64.go',
  'nat64_test.go',
)



test(
  'nat64',
  find_program('go'),
  args: ['test', './...'] + (get_option('b_coverage') ? ['-cover', '-args', '-test.gocoverdir=' + meson.current_build_dir()]: []),
  workdir: meson.current_source_dir(),
  timeout: 120,
  env: get_option('b_coverage') ? {
    'CGO_CFLAGS': '--coverage',
    'CGO_LDFLAGS': '--coverage'
  } : {},
)