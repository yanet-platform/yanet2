nat64_protoc_gen = custom_target(
  'nat64-protoc',
  output: 'nat64.protoset',
  depfile: 'nat64.protoset.d',
  input: meson.current_source_dir() / 'v1/nat64.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += nat64_protoc_gen
