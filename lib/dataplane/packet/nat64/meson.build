# Shared RFC7915 translation core used by the NAT64 dataplane modules
# (stateless today, stateful to come): header/ICMP translation, checksums
# and MTU handling, parameterized by the owning module's lookup policy.

lib_nat64_packet_sources = files(
	'nat64_translate.c',
)

lib_nat64_packet = static_library(
	'nat64_packet',
	lib_nat64_packet_sources,
	dependencies: [
		lib_common_dep,
		libdpdk_inc_dep,
	],
	install: false,
)

lib_nat64_packet_dep = declare_dependency(
	link_with: lib_nat64_packet,
	include_directories: yanet_rootdir,
	dependencies: [
		lib_common_dep,
		libdpdk_inc_dep,
	],
)
