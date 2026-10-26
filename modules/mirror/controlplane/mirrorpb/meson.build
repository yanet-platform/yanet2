mirror_protoc_gen = custom_target(
  'mirror-protoc',
  output: 'mirror.protoset',
  depfile: 'mirror.protoset.d',
  input: meson.current_source_dir() / 'v1/mirror.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += mirror_protoc_gen
