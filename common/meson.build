lib_common_dep = declare_dependency(
  include_directories: [yanet_rootdir],
  # common/memory.h reports failures and polls for cancellation.
  dependencies: [lib_errors_dep, lib_cancellation_dep],
)

if not dataplane_only
  subdir('commonpb')
  subdir('filterpb')
  subdir('readinesspb')
endif

