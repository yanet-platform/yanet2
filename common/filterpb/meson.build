filter_protoc_gen = custom_target(
  'filter-protoc',
  output: 'filter.protoset',
  depfile: 'filter.protoset.d',
  input: meson.current_source_dir() / 'v1/filter.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += filter_protoc_gen
