dependencies = []

dependencies += libdpdk.get_variable('dpdk_dep')

dependencies += libcommon_dep
dependencies += libfilter_dep
dependencies += libpacket_dep
dependencies += libdataplane_dep

sources = files('acl.c',
                'kernel.c',
                'route.c')

libmodules = static_library('modules',
           sources,
           dependencies: dependencies,
           install: false)

libmodules_dep = declare_dependency(
    link_with: libmodules
)
