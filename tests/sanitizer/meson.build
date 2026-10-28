# The address sanitizer runtime of the build must not race global registration
# against thread start, which Go test binaries built with -asan rely on.
#
# The asan suite builds with clang for that reason, so the test runs in clang
# ASan builds; the gcc 14 runtime races them, but gcc ASan builds run no Go
# test with -asan.
if b_sanitize.contains('address') and cc.get_id() == 'clang'
  asan_globals_race_test = executable(
    'asan_globals_race_test',
    ['asan_globals_race_test.c'],
    dependencies: [dependency('threads')],
  )

  test(
    'asan_globals_race',
    asan_globals_race_test,
    args: ['5'],
    suite: ['sanitizer'],
    timeout: 60,
  )
endif
