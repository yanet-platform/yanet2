//go:build yanet_dataplane_rust

package dataplaneut

/*
// The Rust device types live in archives that only a dataplane built with
// the Rust builtins produces, so linking them is opt-in through the
// yanet_dataplane_rust build tag.
#cgo LDFLAGS: -L../../../build/lib/rust
#cgo LDFLAGS: -Wl,--start-group
#cgo LDFLAGS: -lyanet_dp_builtins -lyanet_dp_shim
#cgo LDFLAGS: -Wl,--end-group

struct device;

extern struct device *new_device_vxlan(void);

// keep_rust_refs forces the linker to extract the Rust device constructors
// from their archive, where nothing else references them.
void
keep_rust_refs(void **ptrs) {
	static void *funcs[] = {
		new_device_vxlan,
	};

	*ptrs = funcs;
}
*/
import "C"
