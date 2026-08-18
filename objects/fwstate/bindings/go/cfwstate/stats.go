package cfwstate

//#cgo CFLAGS: -I../../../../../
//#include "lib/fwstate/fwmap.h"
import "C"

// MapStats stores per-map statistics reported by fwstate.
type MapStats struct {
	IndexSize        uint32
	ExtraBucketCount uint32
	MaxChainLength   uint32
	LayerCount       uint32
	TotalElements    uint64
	MaxDeadline      uint64
	MemoryUsed       uint64
}

// MapsStats stores IPv4 and IPv6 fwmap statistics.
type MapsStats struct {
	IPv4 MapStats
	IPv6 MapStats
}

// fwmapStatsOrZero returns stats for the given head fwmap, or zero stats
// when head is nil (no map attached).
func fwmapStatsOrZero(head *C.fwmap_t) C.struct_fwmap_stats {
	if head == nil {
		return C.struct_fwmap_stats{}
	}
	return C.fwmap_get_stats(head)
}

func mapStatsFromC(stats C.struct_fwmap_stats) MapStats {
	return MapStats{
		IndexSize:        uint32(stats.index_size),
		ExtraBucketCount: uint32(stats.extra_bucket_count),
		MaxChainLength:   uint32(stats.max_chain_length),
		LayerCount:       uint32(stats.layer_count),
		TotalElements:    uint64(stats.total_elements),
		MaxDeadline:      uint64(stats.max_deadline),
		MemoryUsed:       uint64(stats.memory_used),
	}
}
