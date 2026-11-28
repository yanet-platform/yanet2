package cnat64s

// maxSessionBatch caps the slots one ReadSessions page collects, whatever
// count the caller asks for.
const maxSessionBatch uint32 = 10000

// Session is one live session of a map object's lan table, as the control
// plane read it: the client's original IPv6 flow and the external identity
// it translates to.
//
// A session is a diagnostic snapshot. The dataplane refreshes sessions as
// packets flow, so the lifetime fields and the latches can be a refresh
// behind the moment of the read, and a session can expire right after it
// was returned.
type Session struct {
	// Proto is the IP protocol number, ICMP for both ICMP families.
	Proto uint16
	// ClientAddr, ClientPort and RemoteAddr, RemotePort are the client's
	// original flow: the remote address is the NAT64 prefix followed by
	// the remote IPv4 address.
	ClientAddr [16]byte
	ClientPort uint16
	RemoteAddr [16]byte
	RemotePort uint16
	// ExternalAddr is the public IPv4 address in network byte order, and
	// ExternalPort the public port, that the flow translates to.
	ExternalAddr [4]byte
	ExternalPort uint16
	// Established is set once the remote has answered the TCP session.
	Established bool
	// ClientClosed and RemoteClosed are set once that end has sent a FIN
	// or an RST.
	ClientClosed bool
	RemoteClosed bool
	// CreatedAt and UpdatedAt are dataplane timestamps in nanoseconds,
	// ExpiresAt the latter plus the lifetime of the last refresh.
	CreatedAt uint64
	UpdatedAt uint64
	ExpiresAt uint64
	LastTTLNs uint64
}
