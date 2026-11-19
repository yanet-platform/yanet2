nat64s_protoc_gen = custom_target(
  'nat64s-protoc',
  output: 'nat64s.protoset',
  depfile: 'nat64s.protoset.d',
  input: meson.current_source_dir() / 'v1/nat64s.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += nat64s_protoc_gen
