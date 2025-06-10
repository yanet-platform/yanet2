# Ring buffer unit tests

# Sources for the ring test
ring_test_sources = files(
  'ring_test.c',
)

# Include directories
ring_test_includes = [
  yanet_rootdir,
  '.',
  '../..',
  '../../..',
]

# Build the ring test executable
ring_test_exe = executable(
  'ring_test',
  ring_test_sources,
  include_directories: ring_test_includes,
  dependencies: [lib_logging_dep],
)

# Register the test
test(
  'pdump_ring_unit_test',
  ring_test_exe,
  suite: 'unit',
  timeout: 30,
)
