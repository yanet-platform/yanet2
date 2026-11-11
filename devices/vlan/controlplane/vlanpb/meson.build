vlan_protoc_gen = custom_target(
  'vlan-protoc',
  output: 'vlan.protoset',
  depfile: 'vlan.protoset.d',
  input: meson.current_source_dir() / 'v1/vlan.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += vlan_protoc_gen
