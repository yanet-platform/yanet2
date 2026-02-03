#!/usr/bin/env python3
# -*- coding: utf-8 -*-

from test import *

IP_SERVER1 = "10.0.1.1"

IP_CLIENT = "10.0.2.1"

PORT_PROXY_INT = 32768
PORT_CLIENT = 12380

data_client1 = 'client first'
data_client2 = 'client second'
data_server1 = 'server first'
data_server2 = 'server second'

ts_client = 2983139994
ts_proxy = 1
ts_server = 12345

options_client_syn = [("MSS", 1460), ("SAckOK", ''), ("Timestamp", (ts_client, 0)), ('WScale', 5)]
options_client_ack = [("Timestamp", (1, 2)), ("NOP", ''), ("NOP", '')]
options_server_syn = [("MSS", 1260), ("SAckOK", ''), ("Timestamp", (ts_server, ts_client)), ("NOP", ''), ('WScale', 3)]


# 001 - type 1 - no proxy, no sec

test_001 = ProxyTest(ip_client=IP_CLIENT, ip_server=IP_SERVER1, ip_proxy=IP_SERVER1, start_seq_to_client=ProxyTest.START_SERVER_SEQ, port_proxy=PORT_CLIENT, cport=PORT_CLIENT)

data_type1 = [
	(
		test_001.FromClient((0, None), 'S'),
		test_001.ToServer((0, None), 'S')
	),
    (
		test_001.FromServer((0, 1), 'AS'),
		test_001.ToClient((0, 1), 'AS')
	),
    (
		test_001.FromClient((1, 1), 'A', raw=data_client1),
		test_001.ToServer((1, 1), 'A', raw=data_client1)
	),
    (
		test_001.FromServer((1, 1 + len(data_client1)), 'A', raw=data_server1),
		test_001.ToClient((1, 1 + len(data_client1)), 'A', raw=data_server1)
	),
    (
		test_001.FromClient((1 + len(data_client1), 1 + len(data_server1)), 'A', raw=data_client2),
		test_001.ToServer((1 + len(data_client1), 1 + len(data_server1)), 'A', raw=data_client2)
	),
]

# def WithPadding(pkt):
#     if len(pkt) < 60:
#         pad_len = 60 - len(pkt)
#         pad = Padding()
#         pad.load = '\x00' * pad_len
#         return pkt/pad

# data_type1 = [
#     (
#         Ether(src=ProxyTest.MAC_CLIENT, dst=ProxyTest.MAC_PROXY)/IP(src=IP_CLIENT, dst=IP_SERVER1, ttl=64)/TCP(sport=PORT_CLIENT, dport=ProxyTest.PORT_PROXY_EXT, flags='A', seq=0, ack=None)/Raw(),
# 		WithPadding(Ether(src=ProxyTest.MAC_PROXY, dst=ProxyTest.MAC_CLIENT)/IP(src=ProxyTest.IP_PROXY_INT, dst=IP_SERVER1, ttl=63)/TCP(sport=PORT_CLIENT, dport=ProxyTest.PORT_PROXY_EXT, flags='A', seq=0, ack=None)/Raw())
#     )
# ]

WriteTest("001", data_type1)
