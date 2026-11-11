blackhole_protoc_gen = custom_target(
  'blackhole-protoc',
  output: 'blackhole.protoset',
  depfile: 'blackhole.protoset.d',
  input: meson.current_source_dir() / 'v1/blackhole.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += blackhole_protoc_gen
