route_protoc_gen = custom_target(
  'route-protoc',
  output: 'route.protoset',
  depfile: 'route.protoset.d',
  input: meson.current_source_dir() / 'v1/route.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += route_protoc_gen
