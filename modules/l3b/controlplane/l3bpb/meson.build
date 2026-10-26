l3b_protoc_gen = custom_target(
  'l3b-protoc',
  output: 'l3b.protoset',
  depfile: 'l3b.protoset.d',
  input: meson.current_source_dir() / 'v1/l3b.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += l3b_protoc_gen
