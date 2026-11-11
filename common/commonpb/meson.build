common_protoc_gen = custom_target(
  'common-protoc',
  output: 'common.protoset',
  input: [
    meson.current_source_dir() / 'v1/target.proto',
    meson.current_source_dir() / 'v1/metric.proto',
    meson.current_source_dir() / 'v1/macaddr.proto',
    meson.current_source_dir() / 'v1/ipaddr.proto',
    meson.current_source_dir() / 'v1/ipv4addr.proto',
    meson.current_source_dir() / 'v1/ipv6addr.proto',
    meson.current_source_dir() / 'v1/iprange.proto',
    meson.current_source_dir() / 'v1/ipnetwork.proto',
    meson.current_source_dir() / 'v1/ipprefix.proto',
    meson.current_source_dir() / 'v1/ipv4prefix.proto',
    meson.current_source_dir() / 'v1/ipv4network.proto',
    meson.current_source_dir() / 'v1/ipv6network.proto',
    meson.current_source_dir() / 'v1/ipv6prefix.proto',
  ],
  command: go_protoc + ['@INPUT@'],
  build_by_default: true,
)
go_proto_targets += common_protoc_gen
