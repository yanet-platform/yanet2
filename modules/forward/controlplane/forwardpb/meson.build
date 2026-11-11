forward_protoc_gen = custom_target(
  'forward-protoc',
  output: 'forward.protoset',
  depfile: 'forward.protoset.d',
  input: meson.current_source_dir() / 'v1/forward.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += forward_protoc_gen
