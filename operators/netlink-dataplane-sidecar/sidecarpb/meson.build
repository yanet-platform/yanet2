proto_dir = join_paths(meson.current_source_dir(), 'v1')
root_dir = meson.project_source_root()
proto_files = [join_paths(proto_dir, 'sidecar.proto')]

netlink_dataplane_sidecar_protoc_gen = custom_target(
    'netlink-dataplane-sidecar-protoc',
    output: ['sidecar.pb.go', 'sidecar_grpc.pb.go'],
    input: proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + root_dir,
        '--go-grpc_out=paths=source_relative:' + root_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
