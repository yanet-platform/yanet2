package main

//#cgo CFLAGS: -I../../ -I../../lib
//#cgo LDFLAGS: -L../../build/modules/route/ -lroute_cp
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#include "api/agent.h"
//#include "modules/route/controlplane.h"
import "C"

func main() {
    agent := C.agent_connect(
    	C.CString("/dev/hugepages/data-0"),
	C.CString("route"),
	16 * 1024 * 1024,
    )

    rmc := C.route_module_config_init(agent, C.CString("route0"))
    C.route_module_config_add_route(
        rmc,
	C.struct_ether_addr{
	    addr: [6]C.uint8_t{0x7c, 0x1c, 0xf1, 0x8c, 0xa7, 0x96,},
	},
	C.struct_ether_addr{
            addr: [6]C.uint8_t{0x24, 0x8a, 0x07, 0x8f, 0x9c, 0xb0,},
	},
    )

    C.route_module_config_add_route_list(
    	rmc,
	1,
	&([]C.uint64_t{0})[0],
    );

    C.route_module_config_add_prefix_v4(
	rmc,
	&([]C.uint8_t{0x00, 0x00, 0x00, 0x00})[0],
	&([]C.uint8_t{0xff, 0xff, 0xff, 0xff})[0],
	0,
    );

    C.route_module_config_add_prefix_v6(
	rmc,
	&([]C.uint8_t{
	    0x00, 0x00, 0x00, 0x00,
	    0x00, 0x00, 0x00, 0x00,
	    0x00, 0x00, 0x00, 0x00,
	    0x00, 0x00, 0x00, 0x00,
        })[0],
	&([]C.uint8_t{
	    0xff, 0xff, 0xff, 0xff,
	    0xff, 0xff, 0xff, 0xff,
	    0xff, 0xff, 0xff, 0xff,
	    0xff, 0xff, 0xff, 0xff,
	})[0],
	0,
    );

    configs := [1]*C.struct_module_data{rmc}

    C.agent_update_modules(
    	agent,
	1,
	&configs[0],
    );

}
