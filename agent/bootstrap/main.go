package main

//#cgo CFLAGS: -I../../ -I../../lib
//#cgo LDFLAGS: -L../../build/modules/forward/ -lforward_cp
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#include "api/agent.h"
//#include "modules/forward/controlplane.h"
//
import "C"

func main() {
	agent := C.agent_connect(
		C.CString("/dev/hugepages/data-0"),
		C.CString("route"),
		1*1024*1024,
	)

	from_krn := C.forward_module_config_init(agent, C.CString("from_krn"))
	C.forward_module_config_enable_v4(
		from_krn,
		&([]C.uint8_t{0x00, 0x00, 0x00, 0x00})[0],
		&([]C.uint8_t{0xff, 0xff, 0xff, 0xff})[0],
	)

	C.forward_module_config_enable_v6(
		from_krn,
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
	)

	to_krn := C.forward_module_config_init(agent, C.CString("to_krn"))
	C.forward_module_config_enable_v4(
		to_krn,
		&([]C.uint8_t{141, 8, 128, 252})[0],
		&([]C.uint8_t{141, 8, 128, 25})[0],
	)
	C.forward_module_config_enable_v4(
		to_krn,
		&([]C.uint8_t{87, 250, 234, 129})[0],
		&([]C.uint8_t{87, 250, 234, 129})[0],
	)

	C.forward_module_config_enable_v6(
		to_krn,
		&([]C.uint8_t{
			0x2a, 0x02, 0x06, 0xb8,
			0x00, 0x00, 0x03, 0x20,
			0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x0b, 0x2b})[0],
		&([]C.uint8_t{
			0x2a, 0x02, 0x06, 0xb8,
			0x00, 0x00, 0x03, 0x20,
			0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x0b, 0x2b})[0],
	)

	C.forward_module_config_enable_v6(
		to_krn,
		&([]C.uint8_t{
			0x2a, 0x02, 0x06, 0xb8,
			0x00, 0x00, 0x03, 0x00,
			0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x0b, 0x2b})[0],
		&([]C.uint8_t{
			0x2a, 0x02, 0x06, 0xb8,
			0x00, 0x00, 0x03, 0x00,
			0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x0b, 0x2b})[0],
	)

	C.forward_module_config_enable_v6(
		to_krn,
		&([]C.uint8_t{
			0xfe, 0x00, 0x00, 0x00,
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
	)

	configs := [2]*C.struct_module_data{from_krn, to_krn}

	C.agent_update_modules(
		agent,
		2,
		&configs[0],
	)

	pipeline0 := C.pipeline_config_create(1)
	defer C.pipeline_config_free(pipeline0)
	C.pipeline_config_set_module(pipeline0, 0, C.CString("forward"), C.CString("to_krn"))

	pipeline1 := C.pipeline_config_create(1)
	defer C.pipeline_config_free(pipeline1)
	C.pipeline_config_set_module(pipeline1, 0, C.CString("forward"), C.CString("from_krn"))

	C.agent_update_pipelines(
		agent,
		2,
		&([2]*C.struct_pipeline_config{
			pipeline0,
			pipeline1,
		})[0],
	)
}
