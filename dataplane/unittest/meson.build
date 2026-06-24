subdir('config')

pending_ring_test = executable(
  'pending-ring-test',
  sources: files('pending_ring_test.c'),
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  include_directories: [yanet_rootdir, '..'],
)
test('worker pending ring', pending_ring_test)
