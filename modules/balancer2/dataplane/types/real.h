#pragma once

#include "common/network.h"

struct real_ip {
	struct net_addr addr;
	enum ip_family family;
};