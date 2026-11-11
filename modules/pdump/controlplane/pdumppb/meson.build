pdump_protoc_gen = custom_target(
  'pdump-protoc',
  output: 'pdump.protoset',
  depfile: 'pdump.protoset.d',
  input: meson.current_source_dir() / 'v1/pdump.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += pdump_protoc_gen
