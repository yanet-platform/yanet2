dependencies = []

dependencies += lib_common_dep

sources = files(
  'ipfw.c',
  'filter.c',
  'attribute.c',
  'helper.c'
)

lib_filter = static_library(
  'filter',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_filter_dep = declare_dependency(
  link_with: lib_filter,
  include_directories: include_directories('.'),
)

test_filter_sources = sources + ['test.c']

test_args = [
  '-O0',
  '-g'
]

executable(
    'test_filter', 
  test_filter_sources, 
  c_args: test_args, 
  link_args: yanet_link_args, 
  dependencies: dependencies,
  link_language: 'c'
)