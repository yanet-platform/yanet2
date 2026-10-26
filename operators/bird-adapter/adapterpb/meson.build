bird_adapter_protoc_gen = custom_target(
  'bird-adapter-protoc',
  output: 'bird-adapter.protoset',
  depfile: 'bird-adapter.protoset.d',
  input: meson.current_source_dir() / 'v1/bird_import.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += bird_adapter_protoc_gen
