package balancer

import (
	"encoding/binary"
	"time"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/relptr"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ListSessions iterates the session table bucket-by-bucket and calls yield
// for each session that matches the filter. Iteration stops early if yield
// returns a non-nil error (e.g. gRPC stream cancelled).
func (b *Balancer) ListSessions(
	filter *balancerpb.Filter,
	now time.Time,
	yield func(*balancerpb.Session) error,
) error {
	if err := validateFilter(filter); err != nil {
		return err
	}

	matcher := newFilterMatcher(filter)
	st := relptr.Deref(&b.handler.Session_table)
	services := relptr.Slice(&b.handler.Vs, b.handler.Vs_count)

	iter := st.newSessionIter()
	unixNow := uint32(now.Unix())

	var buf [bucketMaxEntries]SessionEntry

	for {
		count := iter.nextBucket(unixNow, buf[:])
		if count < 0 {
			break
		}
		for i := range count {
			entry := &buf[i]

			session, ok := resolveSession(entry, services, &matcher)
			if !ok {
				continue
			}

			if err := yield(session); err != nil {
				return err
			}
		}
	}

	return nil
}

// resolveSession converts a raw session entry into a protobuf Session,
// applying filter checks. Returns false if the session is stale or filtered out.
func resolveSession(
	entry *SessionEntry,
	services []VS,
	matcher *filterMatcher,
) (*balancerpb.Session, bool) {
	vsStableIdx := entry.Id.Vs_stable_idx
	vsConfigIdx := configIndexOf(vsStableIdx)

	// No bounds check needed here:
	// - vsConfigIdx is guaranteed to be within bounds because VS array never shrinks.
	vs := &services[vsConfigIdx]
	if vs.isRemoved() || vs.Stable_idx != vsStableIdx {
		return nil, false
	}

	vsID := vs.id()
	if matcher.hasVsFilter && !matcher.matchVsID(vsID) {
		return nil, false
	}

	realStableIdx := entry.State.Real_stable_idx
	realConfigIdx := configIndexOf(realStableIdx)
	reals := relptr.Slice(&vs.Reals, vs.Reals_count)

	r := &reals[realConfigIdx]
	if r.isRemoved() || r.Stable_idx != realStableIdx {
		return nil, false
	}

	realRelID := r.id()
	if matcher.hasRealFilter && !matcher.matchRealID(realRelID) {
		return nil, false
	}

	clientAddrLen := 16
	if vs.Ip_proto == ipprotoIP {
		clientAddrLen = 4
	}
	clientAddr := make([]byte, clientAddrLen)
	copy(clientAddr, entry.Id.Client_ip[:clientAddrLen])

	// client_port is stored in network byte order (copied directly from the
	// TCP/UDP header by the dataplane). Convert to host byte order.
	portBytes := (*[2]byte)(unsafe.Pointer(&entry.Id.Client_port))
	clientPort := binary.BigEndian.Uint16(portBytes[:])

	createTimestamp := time.Unix(int64(entry.State.Create_timestamp), 0)
	lastPacketTimestamp := time.Unix(int64(entry.State.Last_packet_timestamp), 0)
	timeout := time.Duration(entry.State.Timeout) * time.Second

	return &balancerpb.Session{
		ClientAddr:          clientAddr,
		ClientPort:          uint32(clientPort),
		VsId:                vsID,
		RealId:              realRelID,
		CreateTimestamp:     timestamppb.New(createTimestamp),
		LastPacketTimestamp: timestamppb.New(lastPacketTimestamp),
		Timeout:             durationpb.New(timeout),
	}, true
}
