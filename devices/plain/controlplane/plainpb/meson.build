plain_protoc_gen = custom_target(
  'plain-protoc',
  output: 'plain.protoset',
  depfile: 'plain.protoset.d',
  input: meson.current_source_dir() / 'v1/plain.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += plain_protoc_gen
