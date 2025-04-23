# common dependecies
dependencies += lib_common_dep

# dataplane dependecies
dp_dependencies = dependencies
dp_dependencies += lib_packet_dp_dep

dp_dependencies += lib_module_dp_dep

dp_sources = files(
    'dataplane.c',
)

lib_decap_dp = static_library(
    'decap_dp',
    dp_sources,
    c_args: yanet_c_args,
    link_args: yanet_link_args,
    dependencies: dp_dependencies,
    install: false,
)

lib_decap_dp_dep = declare_dependency(
    link_with: lib_decap_dp,
    link_args: [
        '-Wl,--defsym',
        '-Wl,new_module_decap=new_module_decap',
        '-Wl,--export-dynamic-symbol=new_module_decap',
    ],
)
