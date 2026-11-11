common_readiness_protoc_gen = custom_target(
  'readiness-protoc',
  output: 'readiness.protoset',
  depfile: 'readiness.protoset.d',
  input: meson.current_source_dir() / 'v1/readiness.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += common_readiness_protoc_gen
