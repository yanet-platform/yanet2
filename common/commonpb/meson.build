common_proto_dir = meson.current_source_dir()
root_dir = meson.project_source_root()

common_proto_files = [
    join_paths(common_proto_dir, 'v1', 'target.proto'),
    join_paths(common_proto_dir, 'v1', 'metric.proto'),
    join_paths(common_proto_dir, 'v1', 'macaddr.proto'),
    join_paths(common_proto_dir, 'v1', 'ipaddr.proto'),
    join_paths(common_proto_dir, 'v1', 'ipv4addr.proto'),
    join_paths(common_proto_dir, 'v1', 'ipv6addr.proto'),
    join_paths(common_proto_dir, 'v1', 'iprange.proto'),
    join_paths(common_proto_dir, 'v1', 'ipnetwork.proto'),
    join_paths(common_proto_dir, 'v1', 'ipprefix.proto'),
    join_paths(common_proto_dir, 'v1', 'ipv4prefix.proto'),
    join_paths(common_proto_dir, 'v1', 'ipv4network.proto'),
    join_paths(common_proto_dir, 'v1', 'ipv6network.proto'),
    join_paths(common_proto_dir, 'v1', 'ipv6prefix.proto'),
]

# Generate protobuf files. The root include dir is required so that protos
# in this directory can import each other via the canonical path that
# consumers outside this directory use.
common_protoc_gen = custom_target(
    'common-protoc',
    output: 'common.protoset',
    input: common_proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + root_dir,
        '--descriptor_set_out=@OUTPUT@',
        '@INPUT@',
    ],
    build_by_default: true,
)
go_proto_targets += common_protoc_gen
