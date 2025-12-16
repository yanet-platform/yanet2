test_env = environment(
  {
    'MALLOC_PERTURB_': '0',
    'CGO_CFLAGS': '--coverage',
    'CGO_LDFLAGS': '--coverage',
    'ASAN_OPTIONS': '',
    'UBSAN_OPTIONS': '',
    'MSAN_OPTIONS': '',
  },
)

sources = files(
  'fwstate.go',
  'fwstate_test.go',
)

test('fwstate',
    go,
    args: ['test', '-count=1', '-v', './...'] + (get_option('b_coverage') ? ['-cover', '-args', '-test.gocoverdir=' + meson.current_build_dir()]: []),
    workdir: meson.current_source_dir(),
    env: test_env,
    depends: [
        lib_fwstate,
        lib_fwstate_dp,
    ],
    timeout: 120,
)
