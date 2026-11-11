ynpb_gen = custom_target(
  'ynpb-protoc',
  output: 'ynpb.protoset',
  input: [
    meson.current_source_dir() / 'v1/gateway.proto',
    meson.current_source_dir() / 'v1/logging.proto',
    meson.current_source_dir() / 'v1/inspect.proto',
    meson.current_source_dir() / 'v1/function.proto',
    meson.current_source_dir() / 'v1/pipeline.proto',
    meson.current_source_dir() / 'v1/device.proto',
    meson.current_source_dir() / 'v1/counters.proto',
    meson.current_source_dir() / 'v1/auth.proto',
    meson.current_source_dir() / 'v1/readiness.proto',
    meson.current_source_dir() / 'v1/metrics.proto',
    meson.current_source_dir() / 'v1/port.proto',
    meson.current_source_dir() / 'v1/memory.proto',
  ],
  command: go_protoc + ['@INPUT@'],
  # protoc refuses --dependency_out with several inputs, so imports from
  # other targets are tracked through their descriptor sets instead.
  depends: [common_protoc_gen, common_readiness_protoc_gen],
  build_by_default: true,
)
go_proto_targets += ynpb_gen
