package filter

import (
	"runtime"
	"unsafe"

	"github.com/yanet-platform/xnetip"
)

// CBuildDevices writes the C representation of Devices into dst.
func CBuildDevices[T any](dst *T, m Devices, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(m.cBuild(pinner)))
}

// CBuildNet4s writes the C representation of IPv4 IPNets into dst.
func CBuildNet4s[T any](dst *T, m []xnetip.Contiguous[xnetip.Network4], pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(cBuildNet4s(m, pinner)))
}

// CBuildNet6s writes the C representation of IPv6 IPNets into dst.
func CBuildNet6s[T any](dst *T, m []xnetip.BiContiguous, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(cBuildNet6s(m, pinner)))
}

// CBuildPortRanges writes the C representation of PortRanges into dst.
func CBuildPortRanges[T any](dst *T, m PortRanges, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(m.cBuild(pinner)))
}

// CBuildProtoRanges writes the C representation of ProtoRanges into dst.
func CBuildProtoRanges[T any](dst *T, m ProtoRanges, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(m.cBuild(pinner)))
}

// CBuildVlanRanges writes the C representation of VlanRanges into dst.
func CBuildVlanRanges[T any](dst *T, m VlanRanges, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(m.cBuild(pinner)))
}
