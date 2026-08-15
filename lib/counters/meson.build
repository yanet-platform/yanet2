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
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_counters_dep = declare_dependency(
  link_with: [lib_counters],
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)

if not dataplane_only
  # Counter-name pattern matching is a separate target on purpose.
  #
  # lib_counters_dep reaches the dataplane binary through lib/dataplane's
  # config and worker libraries, and the dataplane never filters counters
  # by name. Keeping the matcher apart is what stops a regex engine being
  # linked into the packet path.
  #
  # The whole block is control-plane only: resolving rure pulls in a Rust
  # toolchain, which a dataplane-only build promises not to need.
  #
  # Patterns are answered by string comparison wherever the shape allows,
  # and by a linear-time automaton otherwise.
  rure_dep = dependency('rure')

  # The engine archive itself, for targets meson cannot see the link line of.
  #
  # A Go binary names -lrure through cgo, which is a plain string to meson,
  # so nothing orders the two. Depending on lib_counter_pattern is not
  # enough either: a static library records what it will be linked against
  # rather than linking it. Empty when rure came from the system, where
  # there is no target to wait for.
  librure_targets = []
  if rure_dep.type_name() == 'internal'
    librure_targets = [subproject('regex').get_variable('librure')]
  endif

  lib_counter_pattern = static_library(
    'counter_pattern',
    files('counter_pattern.c'),
    c_args: yanet_c_args,
    link_args: yanet_link_args,
    dependencies: [lib_counters_dep, rure_dep],
    install: false,
  )

  # A static archive carries no link flags of its own, so the librure flags
  # ride on the dependency and reach whatever links the matcher.
  lib_counter_pattern_dep = declare_dependency(
    link_with: [lib_counter_pattern],
    include_directories: yanet_rootdir,
    dependencies: [lib_counters_dep, rure_dep],
  )

  test_counter_pattern = executable(
    'counter_pattern_test',
    'counter_pattern_test.c',
    c_args: yanet_c_args,
    link_args: yanet_link_args,
    dependencies: [lib_counter_pattern_dep, lib_common_dep, lib_logging_dep],
    include_directories: yanet_rootdir,
    link_language: 'c',
  )
  test('counter_pattern', test_counter_pattern, suite: ['lib'])
endif

# Tests
test_counters_histogram = executable(
  'counters_test',
  'tests.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_counters_dep, lib_common_dep, lib_logging_dep],
  include_directories: yanet_rootdir,
  link_language: 'c',
)
test('counters', test_counters_histogram, suite: ['lib'])

counter_storage_test = executable(
  'counter_storage_test',
  'storage_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_counters_dep, lib_common_dep, lib_logging_dep],
  include_directories: yanet_rootdir,
  link_language: 'c',
)
test('counter_storage_oom', counter_storage_test, suite: ['lib'])
