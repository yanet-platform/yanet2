# Ring buffer unit tests

# Sources for the ring test
ring_test_sources = files(
  'ring_test.c',
)

# Include directories
ring_test_includes = [
  yanet_rootdir,
  '..',
  '../..',
]

ring_test_exe = executable(
  'ring_test',
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  sources: ring_test_sources,
  include_directories: ring_test_includes,
  dependencies: [lib_logging_dep],
)

test(
  'pdump_ring',
  ring_test_exe,
  timeout: 30,
)
