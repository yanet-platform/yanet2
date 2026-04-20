lib_common_dep = declare_dependency(
  include_directories: [yanet_rootdir],
)

if not dataplane_only
  subdir('commonpb')
  subdir('filterpb')
endif
subdir('tls_stack')

