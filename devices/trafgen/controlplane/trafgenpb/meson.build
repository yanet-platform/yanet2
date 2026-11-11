trafgen_protoc_gen = custom_target(
  'trafgen-protoc',
  output: 'trafgen.protoset',
  depfile: 'trafgen.protoset.d',
  input: meson.current_source_dir() / 'v1/trafgen.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += trafgen_protoc_gen
