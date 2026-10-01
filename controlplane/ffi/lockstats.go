package ffi

//#cgo CFLAGS: -I../../
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../build/lib/dataplane/pipeline -lpipeline
//#cgo LDFLAGS: -L../../build/lib/errors -lerrors
//#include "api/agent.h"
//#include "api/info.h"
import "C"

// ConfigLockSiteStats is a snapshot of one API site's configuration-lock
// instrumentation counters: how many acquisitions that entry point made,
// and how long it waited for the lock and held it.
type ConfigLockSiteStats struct {
	// Name identifies the API entry point the counters are attributed
	// to, e.g. "cp_config_update_modules".
	Name string
	// Acquisitions is the number of lock acquisitions the site made.
	Acquisitions uint64
	// WaitTotalNS is the total time the site spent waiting for the lock.
	WaitTotalNS uint64
	// WaitMaxNS is the longest single wait the site made.
	WaitMaxNS uint64
	// HoldTotalNS is the total time the site held the lock.
	HoldTotalNS uint64
	// HoldMaxNS is the longest single hold the site made.
	HoldMaxNS uint64
}

// ConfigLockStats returns the per-site snapshots of the controlplane
// configuration-lock instrumentation for the dataplane instance.
//
// The instance must have finished initialising its shared memory (see
// SharedMemory.DataplaneReady); the counters are advisory and aggregated
// across every process attached to the zone.
func (m *DPConfig) ConfigLockStats() []ConfigLockSiteStats {
	statsInfo := C.yanet_get_cp_config_lock_stats(m.ptr)
	if statsInfo == nil {
		return nil
	}
	defer C.cp_config_lock_stats_info_free(statsInfo)

	out := make([]ConfigLockSiteStats, 0, statsInfo.site_count)
	for idx := C.uint64_t(0); idx < statsInfo.site_count; idx++ {
		var site C.struct_cp_config_lock_site_info
		rc := C.yanet_get_cp_config_lock_site_info(statsInfo, idx, &site)
		if rc != 0 {
			panic("FFI corruption: lock stats site index became invalid")
		}

		out = append(out, ConfigLockSiteStats{
			Name:         C.GoString(&site.name[0]),
			Acquisitions: uint64(site.acquisitions),
			WaitTotalNS:  uint64(site.wait_ns),
			WaitMaxNS:    uint64(site.wait_max_ns),
			HoldTotalNS:  uint64(site.hold_ns),
			HoldMaxNS:    uint64(site.hold_max_ns),
		})
	}

	return out
}
