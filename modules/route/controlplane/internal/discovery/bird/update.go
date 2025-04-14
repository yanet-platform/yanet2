package bird

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"unsafe"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/internal/rib"
)

const (
	sizeOfUpdateStruct = unsafe.Sizeof(update{})
	attrAreaSizeOffset = unsafe.Offsetof(update{}.attrsAreaSize)
	sizeOfNetAddrUnion = 40
	sizeOfBaseType     = unsafe.Sizeof(baseType{})
	sizeOfBaseTypeTail = sizeOfNetAddrUnion - sizeOfBaseType

	sizeOfLargeCommunityStruct = int(unsafe.Sizeof(rib.LargeCommunity{}))

	NetIP4  = 1
	NetIP6  = 2
	NetVPN4 = 3
	NetVPN6 = 4
	//NetROA4    = 5
	//NetROA6    = 6
	//NetFLOW4   = 7
	//NetFLOW6   = 8
	//NetIP6SAdr = 9
	//NetMPLS    = 10
	//NetMAX     = 11

	// yabird/proto/export/export.c#L94
	AttrOrigin        AttributeType = 0x01 /* RFC 4271 */ /* WM */
	AttrLocalPref     AttributeType = 0x05 /* WD */
	AttrMultiExitDisc AttributeType = 0x04 /* ON */
	AttrOriginatorID  AttributeType = 0x09 /* RFC 4456 */ /* ON */

	AttrASPath         AttributeType = 0x02 /* WM */
	AttrNextHop        AttributeType = 0x03 /* WM */
	AttrCommunity      AttributeType = 0x08 /* RFC 1997 */ /* OT */
	AttrExtCommunity   AttributeType = 0x10 /* RFC 4360 */
	AttrLargeCommunity AttributeType = 0x20 /* RFC 8092 */
	AttrMPLSLabelStack AttributeType = 0xfe /* MPLS label stack transfer attribute */
	AttrClusterList    AttributeType = 0x0a /* RFC 4456 */ /* ON */

	// AtomicAggr     AttributeType = 0x06 /* WD */
	// Aggregator     AttributeType = 0x07 /* OT */
	// MPReachNLRI    AttributeType = 0x0e /* RFC 4760 */
	// MPUnreachNLRI  AttributeType = 0x0f /* RFC 4760 */
	// AS4Path        AttributeType = 0x11 /* RFC 6793 */
	// AS4Aggregator  AttributeType = 0x12 /* RFC 6793 */
	// AIGP           AttributeType = 0x1a /* RFC 7311 */
	// OnlyToCustomer AttributeType = 0x23 /* RFC 9234 */

	// ASPathSet      = 1 /* Types of path segments */
	ASPathSequence       = 2
	ASPathConfedSequence = 3
	// ASPathConfedSet      = 4

	OpInsert Operation = 1
	OpRemove Operation = 2
)

var (
	ErrUpdateDecode        = errors.New("decode error")
	ErrUnsupportedPrefix   = fmt.Errorf("unsupported prefix type: %w", ErrUpdateDecode)
	ErrDataTooSmall        = fmt.Errorf("data buf is too small: %w", ErrUpdateDecode)
	ErrUnknownAddrUnion    = fmt.Errorf("unknown addr union: %w", ErrUpdateDecode)
	ErrAttributesTruncated = fmt.Errorf("attributes area truncated: %w", ErrUpdateDecode)
	ErrAttrsUnexpectedEOD  = fmt.Errorf("unexpected End Of Data: %w", ErrUpdateDecode)
	ErrBadPrefix           = fmt.Errorf("bad prefix: %w", ErrUpdateDecode)

	ErrUnsupportedRDType = errors.New("ErrUnsupportedRDType")
)

type AttributeType uint8

func (m AttributeType) String() string {
	switch m {
	case AttrOrigin:
		return "ORIGIN"
	case AttrLocalPref:
		return "LOCAL_PREF"
	case AttrMultiExitDisc:
		return "MED"
	case AttrOriginatorID:
		return "ORIGINATOR_ID"
	case AttrASPath:
		return "AS_PATH"
	case AttrNextHop:
		return "NEXT_HOP"
	case AttrCommunity:
		return "COMMUNITY"
	case AttrExtCommunity:
		return "EXT_COMMUNITY"
	case AttrLargeCommunity:
		return "LARGE_COMMUNITY"
	case AttrMPLSLabelStack:
		return "MPLS_LABEL_STACK"
	case AttrClusterList:
		return "CLUSTER_LIST"
	default:
		return fmt.Sprintf("UNKNOWN: %x", uint8(m))
	}
}

func (m AttributeType) isU32Attribute() bool {
	switch m {
	case AttrOrigin:
	case AttrOriginatorID:
	case AttrLocalPref:
	case AttrMultiExitDisc:
	default:
		return false
	}
	return true
}

type Operation uint32

func (m Operation) isRemove() bool {
	return m == OpRemove
}

type IP4Addr [4]byte
type IP6Addr [16]byte

type baseType struct {
	typ       uint8
	prefixLen uint8
	length    uint16
}

func (m *baseType) String() string {
	switch m.typ {
	case NetIP4:
		return fmt.Sprintf("ip4/%d", m.prefixLen)
	case NetIP6:
		return fmt.Sprintf("ip6/%d", m.prefixLen)
	case NetVPN4:
		return fmt.Sprintf("vpn4/%d", m.prefixLen)
	case NetVPN6:
		return fmt.Sprintf("vpn6/%d", m.prefixLen)
	}
	return fmt.Sprintf("Unknown(%x)/%d", m.typ, m.prefixLen)
}

type netAddrIP4 struct {
	baseType
	prefix IP4Addr
}

type netAddrIP6 struct {
	baseType
	prefix IP6Addr
}

type netAddrVPN4 struct {
	baseType
	prefix IP4Addr
	rd     uint64
}

type netAddrVPN6 struct {
	baseType
	prefix IP6Addr
	_      uint32 // padding
	rd     uint64
}

type update struct {
	base          baseType
	baseTail      [sizeOfBaseTypeTail]byte // NetAddrUnion data
	opType        Operation
	peerAddr      IP6Addr
	attrsAreaSize uint32
}

func newUpdate(data []byte) (*update, error) {
	if len(data) < int(sizeOfUpdateStruct) {
		return nil, fmt.Errorf("data[:%d] is too small to hold an update(len=%d): %w",
			len(data), sizeOfUpdateStruct, ErrDataTooSmall)
	}

	u := (*update)(unsafe.Pointer(&data[0]))
	if u.attrsAreaSize < uint32(sizeOfUint32) {
		return nil, fmt.Errorf("%w: unexpected attrsAreaSize=%d", ErrUpdateDecode, u.attrsAreaSize)
	}
	actualAttrsAreaSize := len(data[attrAreaSizeOffset:]) // + size of u.attrsAreaSize
	if int64(u.attrsAreaSize) > int64(actualAttrsAreaSize) {
		return nil, fmt.Errorf("attributes area is too small want=%d, actual=%d: %w",
			u.attrsAreaSize, actualAttrsAreaSize, ErrAttributesTruncated)
	}
	return u, nil
}

func (m *update) Decode(route *rib.Route) error {
	if m.base.length > sizeOfNetAddrUnion {
		return fmt.Errorf("update type(%s) is too big: %d > max known size %d: %w",
			m.base.String(), m.base.length, sizeOfNetAddrUnion, ErrUnknownAddrUnion)
	}
	route.Peer = netipAddrFrom4U32(m.peerAddr)
	route.ToRemove = m.opType.isRemove()

	if err := m.decodePrefixAndRD(route); err != nil {
		return fmt.Errorf("%w: update.decodePrefixAndRD: %w", ErrUpdateDecode, err)
	}
	if err := m.decodeAttributes(route); err != nil {
		return fmt.Errorf("%w: update.Attributes: %w", ErrUpdateDecode, err)
	}
	return nil
}

func isSupportedRDType(rd uint64) bool {
	// https://datatracker.ietf.org/doc/html/rfc4364#section-4.2
	return rd>>48 == 1
}

func (m *update) decodePrefixAndRD(route *rib.Route) error {
	var addr netip.Addr
	switch m.base.typ {
	case NetIP4:
		m4 := (*netAddrIP4)(unsafe.Pointer(m))
		addr = netip.AddrFrom4([4]byte{m4.prefix[3], m4.prefix[2], m4.prefix[1], m4.prefix[0]})
	case NetIP6:
		m6 := (*netAddrIP6)(unsafe.Pointer(m))
		addr = netipAddrFrom4U32(m6.prefix)
	case NetVPN4:
		m4 := (*netAddrVPN4)(unsafe.Pointer(m))
		addr = netip.AddrFrom4([4]byte{m4.prefix[3], m4.prefix[2], m4.prefix[1], m4.prefix[0]})
		if ok := isSupportedRDType(m4.rd); !ok {
			return ErrUnsupportedRDType
		}
		route.RD = m4.rd
	case NetVPN6:
		m6 := (*netAddrVPN6)(unsafe.Pointer(m))
		addr = netipAddrFrom4U32(m6.prefix)
		if ok := isSupportedRDType(m6.rd); !ok {
			return ErrUnsupportedRDType
		}
		route.RD = m6.rd
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedPrefix, m.base.String())
	}
	prefix, err := addr.Prefix(int(m.base.prefixLen))
	if err != nil {
		return fmt.Errorf("%w: addr(%s).Prefix(%d): %w", ErrBadPrefix, addr, m.base.prefixLen, err)
	}
	route.Prefix = prefix
	return nil
}

func ExtendedAttributeID(attributeID uint32) AttributeType {
	return AttributeType(attributeID & 0xff)
}

func (m *update) decodeAttributes(route *rib.Route) error {
	if m.attrsAreaSize == uint32(sizeOfUint32) {
		return nil // no attributes
	}

	// SAFETY: newUpdate checks for data boundaries.
	data := unsafe.Slice((*byte)(
		unsafe.Pointer(uintptr(unsafe.Pointer(&m.attrsAreaSize)))),
		m.attrsAreaSize,
	)
	data = data[sizeOfUint32:] // skip m.attrsAreaSize

	for len(data) > int(sizeOfUint32) {

		typ := ExtendedAttributeID(binary.LittleEndian.Uint32(data))
		data = data[sizeOfUint32:]

		if len(data) < int(sizeOfUint32) {
			return fmt.Errorf("unexpected end of data during decoding attribute type %s: %w", typ.String(), ErrAttributesTruncated)
		}

		var attrSize uint32
		if typ.isU32Attribute() {
			attrSize = uint32(sizeOfUint32)
			val := binary.LittleEndian.Uint32(data)
			switch typ {
			case AttrOrigin, AttrOriginatorID:
				// NOTE: currently unused
			case AttrLocalPref:
				route.Pref = val
			case AttrMultiExitDisc:
				route.Med = val
			}
		} else {
			attrSize = binary.LittleEndian.Uint32(data)
			data = data[sizeOfUint32:] // size of chunk size
			if len(data) < int(attrSize) {
				return fmt.Errorf("unexpected end of data len=%d, want=%d: %w", len(data), attrSize, ErrAttributesTruncated)
			}
			if err := m.decodeComplexAttribute(route, data[:attrSize], typ); err != nil {
				return fmt.Errorf("decode attribute %s: %w", typ.String(), err)
			}
		}
		data = data[attrSize:]
	}
	if len(data) != 0 {
		return fmt.Errorf("unhandled attributes data len=%d: %#v: %w", len(data), data, ErrAttrsUnexpectedEOD)
	}
	return nil
}

func (m *update) decodeComplexAttribute(route *rib.Route, data []byte, typ AttributeType) error {
	switch typ {
	case AttrOrigin, AttrLocalPref, AttrMultiExitDisc, AttrOriginatorID:
	case AttrASPath:
		// https://datatracker.ietf.org/doc/html/rfc4271#section-5.1.2
		for len(data) >= 2 { // traverse all segments
			segmentType := data[0]
			route.ASPathLen = data[1]
			if route.ASPathLen == 0 {
				return nil
			}
			data = data[2:]
			asPathBytesSize := int(route.ASPathLen) * int(sizeOfUint32)
			lastUint32Start := asPathBytesSize - int(sizeOfUint32)

			// OriginAS is the last one
			if asPathBytesSize > len(data) {
				return fmt.Errorf("ASPath attribute truncated want=%d, actual=%d: %w",
					asPathBytesSize, len(data), ErrAttrsUnexpectedEOD)
			}
			peerAS := binary.BigEndian.Uint32(data)
			originAS := binary.BigEndian.Uint32(data[lastUint32Start:])
			data = data[asPathBytesSize:]

			if segmentType != ASPathSequence && segmentType != ASPathConfedSequence {
				// return fmt.Errorf("unsupported ASPath segment type: %d", segmentType)
				// Silently skip unsupported AS path segment types (e.g., AS_SET, AS_CONFED_SET).
				// These segment types are valid per RFC 4271, but we only process sequence types
				// for determining peer and origin AS values.
				// Note: Routes with only AS_SET or AS_CONFED_SET segments (no sequence types)
				// will have empty peer/origin AS values, which is acceptable as these routes
				// typically represent aggregated paths where specific AS information is less relevant.
				continue
			}

			route.PeerAS = peerAS
			route.OriginAS = originAS
			// stop decoding upon encountering the first successful match
			return nil
		}
		if len(data) != 0 {
			return fmt.Errorf("unhandled ASPath attribute data len=%d: %#v: %w", len(data), data, ErrAttrsUnexpectedEOD)
		}
	case AttrNextHop:
		switch len(data) {
		case net.IPv6len:
			route.NextHop = netipAddrFrom4U32([16]byte(data[:net.IPv6len]))
		case net.IPv6len * 2: // Link-Local next hop?
			// Try to use the second addr.
			route.NextHop = netipAddrFrom4U32([16]byte(data[net.IPv6len:]))
			if route.NextHop.IsUnspecified() {
				// If the second addr is zero, use the first addr.
				route.NextHop = netipAddrFrom4U32([16]byte(data[:net.IPv6len]))
			}
		}
	case AttrCommunity:
	case AttrExtCommunity:
	case AttrLargeCommunity:
		if len(data) == 0 {
			// skip empty area
			return nil
		}
		areaSize := len(data)
		tailSize := len(data) % sizeOfLargeCommunityStruct
		if tailSize != 0 {
			return fmt.Errorf("%w: area of large communities has unhandled data tail %d bytes: %#+v",
				ErrUpdateDecode, tailSize, data[len(data)-tailSize:])

		}
		largeCommunities := unsafe.Slice(
			(*rib.LargeCommunity)(unsafe.Pointer(&data[0])),
			areaSize/sizeOfLargeCommunityStruct,
		)
		route.LargeCommunities = slices.Clone(largeCommunities)
	case AttrMPLSLabelStack:
	case AttrClusterList:
	default:
		return fmt.Errorf("unexpected attribute: %s", typ.String())
	}
	return nil
}

func netipAddrFrom4U32(b [16]byte) netip.Addr {
	return netip.AddrFrom16([16]byte{
		b[3], b[2], b[1], b[0],
		b[7], b[6], b[5], b[4],
		b[11], b[10], b[9], b[8],
		b[15], b[14], b[13], b[12],
	})

}
