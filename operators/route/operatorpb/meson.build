route_operator_protoc_gen = custom_target(
  'route-operator-protoc',
  output: 'route-operator.protoset',
  input: [
    meson.current_source_dir() / 'v1/operator.proto',
    meson.current_source_dir() / 'v1/route.proto',
    meson.current_source_dir() / 'v1/neighbour.proto',
  ],
  command: go_protoc + ['@INPUT@'],
  # protoc refuses --dependency_out with several inputs, so imports from
  # other targets are tracked through their descriptor sets instead.
  depends: [common_protoc_gen],
  build_by_default: true,
)
go_proto_targets += route_operator_protoc_gen

route_operator_readiness_protoc_gen = custom_target(
  'route-operator-readiness-protoc',
  output: 'route-operator-readiness.protoset',
  depfile: 'route-operator-readiness.protoset.d',
  input: meson.current_source_dir() / 'v1/readiness.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += route_operator_readiness_protoc_gen
