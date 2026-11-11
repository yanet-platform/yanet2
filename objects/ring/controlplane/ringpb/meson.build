ring_protoc_gen = custom_target(
  'ring-protoc',
  output: 'ring.protoset',
  depfile: 'ring.protoset.d',
  input: meson.current_source_dir() / 'v1/ring.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += ring_protoc_gen
