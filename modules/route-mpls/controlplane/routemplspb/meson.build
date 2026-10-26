route_mpls_protoc_gen = custom_target(
  'route-mpls-protoc',
  output: 'route-mpls.protoset',
  depfile: 'route-mpls.protoset.d',
  input: meson.current_source_dir() / 'v1/routempls.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += route_mpls_protoc_gen
