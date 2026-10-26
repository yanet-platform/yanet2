decap_protoc_gen = custom_target(
  'decap-protoc',
  output: 'decap.protoset',
  depfile: 'decap.protoset.d',
  input: meson.current_source_dir() / 'v1/decap.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += decap_protoc_gen
