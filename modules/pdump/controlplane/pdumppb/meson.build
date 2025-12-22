proto_dir = meson.current_source_dir()
proto_files = [join_paths(proto_dir, 'pdump.proto')]

protoc_gen = custom_target(
  'pdump-protoc',
  output: ['pdump.pb.go', 'pdump_grpc.pb.go'],
  input: proto_files,
  command: [
    protoc,
    '-I', proto_dir,
    '--go_out=paths=source_relative:' + proto_dir,
    '--go-grpc_out=paths=source_relative:' + proto_dir,
    '@INPUT@',
  ],
  build_by_default: true,
)
pdump_protoc_gen = protoc_gen
