#pragma once

#include <netinet/in.h>
#include <stdint.h>

struct ether_addr {
	uint8_t addr[6];
};

#define NET4_LEN 4
#define NET6_LEN 16

struct net6_addr {
	uint8_t bytes[NET6_LEN];
};

struct net6_addr_range {
	struct net6_addr start;
	struct net6_addr end;
};

struct net4_addr {
	uint8_t bytes[NET4_LEN];
};

struct net4_addr_range {
	struct net4_addr from;
	struct net4_addr to;
};

struct net_addr {
	union {
		struct net4_addr v4;
		struct net6_addr v6;
	};
};

struct net_addr_range {
	struct net_addr from;
	struct net_addr to;
};

struct net6 {
	uint8_t addr[NET6_LEN];
	uint8_t mask[NET6_LEN];
};

struct net4 {
	uint8_t addr[NET4_LEN];
	uint8_t mask[NET4_LEN];
};

struct net {
	union {
		struct net4 v4;
		struct net6 v6;
	};
};

// Word views over address and mask bytes, safe under strict aliasing and
// at any byte offset.
//
// The byte arrays have no word type of their own and no alignment
// guarantee, so a plain integer pointer cast over them is undefined
// behaviour. These types may alias anything and carry byte alignment,
// which costs nothing on targets with unaligned loads and makes strict
// alignment targets read byte by byte.
typedef uint32_t __attribute__((__may_alias__, __aligned__(1))) net4_word;
typedef uint64_t __attribute__((__may_alias__, __aligned__(1))) net6_half;
_Static_assert(_Alignof(net4_word) == 1, "net4_word must have byte alignment");
_Static_assert(_Alignof(net6_half) == 1, "net6_half must have byte alignment");

enum ip_family {
	ip_family_ip4,
	ip_family_ip6,
};

enum transport_proto { transport_proto_tcp, transport_proto_udp };