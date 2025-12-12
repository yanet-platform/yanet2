sources = files(
  'nat64.go',
  'nat64_test.go',
)

test_env = environment(
  {
    'MALLOC_PERTURB_': '0',
    'CGO_CFLAGS': ' '.join(['--coverage'] + yanet_cgo_cflags),
    'CGO_LDFLAGS': ' '.join(['--coverage'] + yanet_cgo_ldflags),
    'ASAN_OPTIONS': '',
    'UBSAN_OPTIONS': 'halt_on_error=0:print_stacktrace=0', # temporary allow undefined behavior
    'LSAN_OPTIONS': 'detect_leaks=0', # temporary allow leaks
    'MSAN_OPTIONS': '',
  },
)

test(
  'nat64',
  go,
  args: ['test', './...'] + (get_option('b_coverage') ? ['-cover', '-args', '-test.gocoverdir=' + meson.current_build_dir()]: []),
  workdir: meson.current_source_dir(),
  timeout: 120,
  env: test_env,
  depends: [
    lib_nat64_cp,
  ],
)
