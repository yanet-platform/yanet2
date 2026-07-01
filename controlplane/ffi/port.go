package ffi

//#cgo CFLAGS: -I../../
//#include "api/counter.h"
import "C"

import "fmt"

type PortCounter struct {
	Name  string
	Value uint64
}

type PortGroup struct {
	PortID   uint16
	PortName string
	Counters []PortCounter
}

func (m *DPConfig) PortCounters() ([]PortGroup, error) {
	groups := C.yanet_get_port_counters(m.ptr)
	if groups == nil {
		return nil, fmt.Errorf("failed to get port counters")
	}
	defer C.yanet_port_counter_group_list_free(groups)

	result := make([]PortGroup, 0, groups.port_count)
	for groupIdx := range groups.port_count {
		group := C.yanet_get_port_counter_group(groups, groupIdx)
		if group == nil {
			continue
		}

		counters := group.counters
		portGroup := PortGroup{
			PortID:   uint16(group.port_id),
			PortName: C.GoString(&group.port_name[0]),
			Counters: make([]PortCounter, 0, counters.count),
		}
		for idx := range counters.count {
			handle := C.yanet_get_counter(counters, idx)
			if handle == nil {
				continue
			}
			portGroup.Counters = append(
				portGroup.Counters,
				PortCounter{
					Name: C.GoString(&handle.name[0]),
					Value: uint64(C.yanet_get_counter_value(
						handle.value_handle,
						0,
						0,
					)),
				},
			)
		}

		result = append(result, portGroup)
	}

	return result, nil
}
