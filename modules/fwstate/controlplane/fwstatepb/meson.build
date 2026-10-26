fwstate_protoc_gen = custom_target(
  'fwstate-protoc',
  output: 'fwstate.protoset',
  depfile: 'fwstate.protoset.d',
  input: meson.current_source_dir() / 'v1/fwstate.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += fwstate_protoc_gen
