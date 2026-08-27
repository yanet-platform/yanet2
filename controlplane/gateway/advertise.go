package gateway

import "net"

// AdvertisedEndpoint picks the address to register, preferring a configured one
// over the address a listener resolved to.
//
// A resolved address only reaches peers sharing the network namespace, and a
// wildcard one sends them to their own loopback. The configured value is
// returned as written, since the gateway resolves it on each dial.
func AdvertisedEndpoint(advertised string, listen net.Addr) string {
	if advertised != "" {
		return advertised
	}

	return listen.String()
}
