unrdup_protoc_gen = custom_target(
  'unrdup-protoc',
  output: 'unrdup.protoset',
  depfile: 'unrdup.protoset.d',
  input: meson.current_source_dir() / 'v1/unrdup.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += unrdup_protoc_gen
