package module

import (
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

////////////////////////////////////////////////////////////////////////////////

// Timeouts of sessions with different types.
type SessionsTimeouts struct {
	TcpSynAck uint32
	TcpSyn    uint32
	TcpFin    uint32
	Tcp       uint32
	Udp       uint32
	Default   uint32
}

// NewSessionsTimeoutsFromProto creates SessionsTimeouts from protobuf message.
func NewSessionsTimeoutsFromProto(pb *balancerpb.SessionsTimeouts) SessionsTimeouts {
	if pb == nil {
		return SessionsTimeouts{}
	}
	return SessionsTimeouts{
		TcpSynAck: pb.TcpSynAck,
		TcpSyn:    pb.TcpSyn,
		TcpFin:    pb.TcpFin,
		Tcp:       pb.Tcp,
		Udp:       pb.Udp,
		Default:   pb.Default,
	}
}

// IntoProto converts SessionsTimeouts to protobuf message.
func (st SessionsTimeouts) IntoProto() *balancerpb.SessionsTimeouts {
	return &balancerpb.SessionsTimeouts{
		TcpSynAck: st.TcpSynAck,
		TcpSyn:    st.TcpSyn,
		TcpFin:    st.TcpFin,
		Tcp:       st.Tcp,
		Udp:       st.Udp,
		Default:   st.Default,
	}
}
