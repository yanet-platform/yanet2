common_proto_dir = meson.current_source_dir()
root_dir = meson.project_source_root()

common_proto_files = [
    join_paths(common_proto_dir, 'target.proto'),
    join_paths(common_proto_dir, 'metric.proto'),
    join_paths(common_proto_dir, 'macaddr.proto'),
    join_paths(common_proto_dir, 'ipaddr.proto'),
    join_paths(common_proto_dir, 'iprange.proto'),
]

# Generate protobuf files. The root include dir is required so that
# iprange.proto can import ipaddr.proto via the canonical path
# "common/commonpb/ipaddr.proto", which matches the import path used by
# consumers outside this directory.
common_protoc_gen = custom_target(
    'common-protoc',
    output: [
        'target.pb.go',
        'metric.pb.go',
        'macaddr.pb.go',
        'ipaddr.pb.go',
        'iprange.pb.go',
    ],
    input: common_proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + root_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
