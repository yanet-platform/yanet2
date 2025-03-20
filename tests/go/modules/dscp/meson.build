sources = files(
  'dscp.go',
  'dscp_test.go',
)

test(
  'dscp',
  find_program('go'),
  args: ['test', './...'],
  workdir: meson.current_source_dir(),
  timeout: 120,
)
