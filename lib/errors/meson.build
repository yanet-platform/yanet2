sources = files(
  'errors.c',
)

lib_errors = static_library(
  'errors',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  include_directories: [yanet_rootdir],
  install: false,
)

lib_errors_dep = declare_dependency(
  link_with: lib_errors,
  include_directories: [include_directories('.'), yanet_rootdir],
)

if not dataplane_only
  # Content checks also catch restored mtimes and a deleted package marker.
  cerrors_go_cdeps = custom_target(
    'cerrors-go-cdeps',
    input: [
      lib_errors,
      # Thin archives keep member contents in separate object files.
      lib_errors.extract_all_objects(recursive: false),
      files('errors.h'),
    ],
    output: 'cerrors-go-cdeps.stamp',
    command: go_cdeps + [
      '--output', join_paths(meson.project_source_root(), 'bindings', 'go', 'cerrors', 'zz_yanet_cdeps.c'),
      '--stamp', '@OUTPUT@',
      '@INPUT@',
    ],
    build_always_stale: true,
    build_by_default: true,
  )
endif
