sources = files(
  'decap.go',
  'decap_test.go',
)

test(
  'decap',
  find_program('go'),
  args: ['test', './...'],
  workdir: meson.current_source_dir(),
  timeout: 120,
  env: ['CGO_CFLAGS="--coverage"', 'CGO_LDFLAGS="--coverage"'],
)
