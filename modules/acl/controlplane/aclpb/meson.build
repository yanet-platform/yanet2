acl_protoc_gen = custom_target(
  'acl-protoc',
  output: 'acl.protoset',
  depfile: 'acl.protoset.d',
  input: meson.current_source_dir() / 'v1/acl.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += acl_protoc_gen
