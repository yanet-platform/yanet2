dependencies = [
  lib_common_dep,
  lib_errors_dep,
]

sources = files(
  'counters.c',
  'histogram.c'
)

lib_counters = static_library(
  'counters',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_counters_dep = declare_dependency(
  link_with: [lib_counters],
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)

# Counter-name pattern matching is control-plane only: resolving rure pulls in
# a Rust toolchain, which a dataplane-only build promises not to need.
if not dataplane_only
  # The regex engine ships no build meson can consume, so cargo builds it.
  regex_root = meson.project_source_root() / 'subprojects' / 'regex'
  cargo = find_program('cargo', required: true)

  # A cross build must name the target, or the archive is built for the
  # machine running cargo and fails at link.
  #
  # A native build names none, which keeps it working on a toolchain that
  # carries only its own target.
  cargo_target = ''
  cargo_subdir = 'release'
  if meson.is_cross_build()
    rust_triples = {
      'x86_64': 'x86_64-unknown-linux-gnu',
      'aarch64': 'aarch64-unknown-linux-gnu',
    }
    cpu = host_machine.cpu_family()
    if host_machine.system() != 'linux' or not rust_triples.has_key(cpu)
      error('no rust target known for @0@/@1@'.format(host_machine.system(), cpu))
    endif
    cargo_target = '--target=' + rust_triples[cpu]
    cargo_subdir = rust_triples[cpu] / 'release'
  endif

  # Cargo names its own output path and has no stable flag to override it,
  # so the archive is copied to where meson expects it.
  #
  # The crate tree is cargo's to track, so this runs every build and lets
  # cargo decide whether there is work to do.
  librure = custom_target(
    'librure',
    output: 'librure.a',
    command: [
      'sh', '-c',
      # Third-party lints are not actionable here, and a native build
      # passes no target argument at all.
      #
      # --cap-lints is what cargo applies to dependencies on its own and
      # only withholds from the package being built. The target value is
      # left unquoted so an empty one expands to nothing rather than to an
      # empty argument.

      # Preserve unchanged archives so dependent links can stay up to date.
      'set -eu; RUSTFLAGS="--cap-lints allow" ' +
        '"$1" build --release --manifest-path "$2" --target-dir "$3" $5; ' +
        'if ! cmp -s "$3/$6/librure.a" "$4"; then ' +
          'tmp=$(mktemp -d "$4.XXXXXX"); ' +
          'trap \'rm -rf "$tmp"\' 0; ' +
          'cp "$3/$6/librure.a" "$tmp/librure.a"; ' +
          'mv -f "$tmp/librure.a" "$4"; ' +
        'fi',
      'sh',
      cargo,
      regex_root / 'regex-capi' / 'Cargo.toml',
      meson.current_build_dir() / 'rure-target',
      '@OUTPUT@',
      cargo_target,
      cargo_subdir,
    ],
    build_by_default: true,
    build_always_stale: true,
  )

  rure_dep = declare_dependency(
    # A plain flag: include_directories() into another subproject's tree is a
    # sandbox violation that meson is turning into a hard error.
    compile_args: ['-I' + regex_root / 'regex-capi' / 'include'],
    link_with: librure,
    # Reported by rustc --print native-static-libs for this crate.
    link_args: ['-lgcc_s', '-lutil', '-lrt', '-lpthread', '-lm', '-ldl'],
  )

  # The engine archive itself, for targets meson cannot see the link line of.
  #
  # A Go binary built outside this project links the archive through cgo
  # flags, which are plain strings to meson, so nothing orders the two.
  # Depending on lib_counter_pattern is not enough either: a static library
  # records what it will be linked against rather than linking it.
  librure_targets = [librure]

  # Kept apart from the counters library, which the dataplane links but never
  # uses to filter counters by name, so no regex engine reaches the packet path.
  #
  # Patterns are answered by string comparison wherever the shape allows, and
  # by a linear-time automaton otherwise.
  lib_counter_pattern = static_library(
    'counter_pattern',
    files('counter_pattern.c'),
    dependencies: [lib_common_dep, rure_dep],
    install: false,
  )

  # A static archive carries no link flags of its own, so the librure flags
  # ride on the dependency and reach whatever links the matcher.
  lib_counter_pattern_dep = declare_dependency(
    link_with: [lib_counter_pattern],
    include_directories: yanet_rootdir,
    dependencies: [lib_common_dep, rure_dep],
  )

  test_counter_pattern = executable(
    'counter_pattern_test',
    'counter_pattern_test.c',
    dependencies: [lib_counter_pattern_dep, lib_common_dep, lib_logging_dep],
    include_directories: yanet_rootdir,
  )
  test('counter_pattern', test_counter_pattern, suite: ['lib'])
endif

# Tests
test_counters_histogram = executable(
  'counters_test',
  'tests.c',
  dependencies: [lib_counters_dep, lib_common_dep, lib_logging_dep],
  include_directories: yanet_rootdir,
)
test('counters', test_counters_histogram, suite: ['lib'])

counter_storage_test = executable(
  'counter_storage_test',
  'storage_test.c',
  dependencies: [lib_counters_dep, lib_common_dep, lib_logging_dep, lib_errors_dep],
  include_directories: yanet_rootdir,
)
test('counter_storage_oom', counter_storage_test, suite: ['lib'])
