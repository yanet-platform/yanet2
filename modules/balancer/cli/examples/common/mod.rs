//! Common data creation functions for examples

use yanet_cli_balancer::rpc::balancerpb;

#[allow(dead_code)]
pub fn create_show_config_example() -> balancerpb::ShowConfigResponse {
    balancerpb::ShowConfigResponse {
        name: "my-balancer".to_string(),
        config: Some(balancerpb::BalancerConfig {
            packet_handler: Some(balancerpb::PacketHandlerConfig {
                vs: vec![
                    balancerpb::VirtualService {
                        id: Some(balancerpb::VsIdentifier {
                            addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                            port: 80,
                            proto: balancerpb::TransportProto::Tcp as i32,
                        }),
                        scheduler: balancerpb::VsScheduler::SourceHash as i32,
                        allowed_srcs: vec![
                            balancerpb::Net {
                                addr: Some(balancerpb::Addr { bytes: vec![10, 0, 0, 0] }),
                                size: 8,
                            },
                            balancerpb::Net {
                                addr: Some(balancerpb::Addr { bytes: vec![172, 16, 0, 0] }),
                                size: 12,
                            },
                        ],
                        reals: vec![
                            balancerpb::Real {
                                id: Some(balancerpb::RelativeRealIdentifier {
                                    ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 1] }),
                                    port: 80,
                                }),
                                weight: 100,
                                src_addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                                src_mask: Some(balancerpb::Addr { bytes: vec![255, 255, 255, 255] }),
                            },
                            balancerpb::Real {
                                id: Some(balancerpb::RelativeRealIdentifier {
                                    ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 2] }),
                                    port: 80,
                                }),
                                weight: 50,
                                src_addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                                src_mask: Some(balancerpb::Addr { bytes: vec![255, 255, 255, 255] }),
                            },
                        ],
                        flags: Some(balancerpb::VsFlags {
                            gre: true,
                            fix_mss: true,
                            ops: true,
                            pure_l3: false,
                            wlc: false,
                        }),
                        peers: vec![
                            balancerpb::Addr { bytes: vec![192, 0, 2, 10] },
                            balancerpb::Addr { bytes: vec![192, 0, 2, 11] },
                        ],
                    },
                    balancerpb::VirtualService {
                        id: Some(balancerpb::VsIdentifier {
                            addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                            port: 443,
                            proto: balancerpb::TransportProto::Tcp as i32,
                        }),
                        scheduler: balancerpb::VsScheduler::RoundRobin as i32,
                        allowed_srcs: vec![balancerpb::Net {
                            addr: Some(balancerpb::Addr { bytes: vec![0, 0, 0, 0] }),
                            size: 0,
                        }],
                        reals: vec![
                            balancerpb::Real {
                                id: Some(balancerpb::RelativeRealIdentifier {
                                    ip: Some(balancerpb::Addr { bytes: vec![10, 2, 1, 1] }),
                                    port: 443,
                                }),
                                weight: 100,
                                src_addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                                src_mask: Some(balancerpb::Addr { bytes: vec![255, 255, 255, 255] }),
                            },
                            balancerpb::Real {
                                id: Some(balancerpb::RelativeRealIdentifier {
                                    ip: Some(balancerpb::Addr { bytes: vec![10, 2, 1, 2] }),
                                    port: 443,
                                }),
                                weight: 100,
                                src_addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                                src_mask: Some(balancerpb::Addr { bytes: vec![255, 255, 255, 255] }),
                            },
                            balancerpb::Real {
                                id: Some(balancerpb::RelativeRealIdentifier {
                                    ip: Some(balancerpb::Addr { bytes: vec![10, 2, 1, 3] }),
                                    port: 443,
                                }),
                                weight: 50,
                                src_addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                                src_mask: Some(balancerpb::Addr { bytes: vec![255, 255, 255, 255] }),
                            },
                        ],
                        flags: Some(balancerpb::VsFlags {
                            gre: false,
                            fix_mss: true,
                            ops: true,
                            pure_l3: true,
                            wlc: true,
                        }),
                        peers: vec![balancerpb::Addr { bytes: vec![192, 0, 2, 10] }],
                    },
                ],
                source_address_v4: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                source_address_v6: Some(balancerpb::Addr {
                    bytes: vec![0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1],
                }),
                decap_addresses: vec![
                    balancerpb::Addr { bytes: vec![192, 0, 2, 1] },
                    balancerpb::Addr {
                        bytes: vec![0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1],
                    },
                ],
                sessions_timeouts: Some(balancerpb::SessionsTimeouts {
                    tcp_syn_ack: 10,
                    tcp_syn: 10,
                    tcp_fin: 10,
                    tcp: 60,
                    udp: 30,
                    default: 60,
                }),
            }),
            state: Some(balancerpb::StateConfig {
                session_table_capacity: Some(1_000_000),
                refresh_period: Some(prost_types::Duration { seconds: 1, nanos: 0 }),
                session_table_max_load_factor: Some(0.75),
                wlc: Some(balancerpb::WlcConfig {
                    power: Some(10),
                    max_weight: Some(1000),
                }),
            }),
        }),
        buffered_real_updates: vec![balancerpb::RealUpdate {
            real_id: Some(balancerpb::RealIdentifier {
                vs: Some(balancerpb::VsIdentifier {
                    addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                    port: 80,
                    proto: balancerpb::TransportProto::Tcp as i32,
                }),
                real: Some(balancerpb::RelativeRealIdentifier {
                    ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 3] }),
                    port: 80,
                }),
            }),
            enable: Some(true),
            weight: Some(75),
        }],
    }
}

#[allow(dead_code)]
pub fn create_list_configs_example() -> balancerpb::ListConfigsResponse {
    balancerpb::ListConfigsResponse {
        configs: vec!["my-balancer".to_string(), "test-balancer".to_string()],
    }
}

#[allow(dead_code)]
pub fn create_state_info_example() -> balancerpb::ShowInfoResponse {
    balancerpb::ShowInfoResponse {
        name: "my-balancer".to_string(),
        info: Some(balancerpb::BalancerInfo {
            active_sessions: 15234,
            last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315845, nanos: 0 }),
            vs: vec![
                balancerpb::VsInfo {
                    id: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                        port: 80,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    active_sessions: 10234,
                    last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315844, nanos: 0 }),
                    reals: vec![
                        balancerpb::RealInfo {
                            id: Some(balancerpb::RealIdentifier {
                                vs: Some(balancerpb::VsIdentifier {
                                    addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                                    port: 80,
                                    proto: balancerpb::TransportProto::Tcp as i32,
                                }),
                                real: Some(balancerpb::RelativeRealIdentifier {
                                    ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 1] }),
                                    port: 80,
                                }),
                            }),
                            active_sessions: 6000,
                            last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315844, nanos: 0 }),
                        },
                        balancerpb::RealInfo {
                            id: Some(balancerpb::RealIdentifier {
                                vs: Some(balancerpb::VsIdentifier {
                                    addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                                    port: 80,
                                    proto: balancerpb::TransportProto::Tcp as i32,
                                }),
                                real: Some(balancerpb::RelativeRealIdentifier {
                                    ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 2] }),
                                    port: 80,
                                }),
                            }),
                            active_sessions: 4234,
                            last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315843, nanos: 0 }),
                        },
                    ],
                },
                balancerpb::VsInfo {
                    id: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                        port: 443,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    active_sessions: 5000,
                    last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315845, nanos: 0 }),
                    reals: vec![balancerpb::RealInfo {
                        id: Some(balancerpb::RealIdentifier {
                            vs: Some(balancerpb::VsIdentifier {
                                addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                                port: 443,
                                proto: balancerpb::TransportProto::Tcp as i32,
                            }),
                            real: Some(balancerpb::RelativeRealIdentifier {
                                ip: Some(balancerpb::Addr { bytes: vec![10, 2, 1, 1] }),
                                port: 443,
                            }),
                        }),
                        active_sessions: 5000,
                        last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315845, nanos: 0 }),
                    }],
                },
            ],
        }),
    }
}

#[allow(dead_code)]
pub fn create_config_stats_example() -> balancerpb::ShowStatsResponse {
    balancerpb::ShowStatsResponse {
        name: "my-balancer".to_string(),
        r#ref: Some(balancerpb::PacketHandlerRef {
            device: Some("eth0".to_string()),
            pipeline: Some("main".to_string()),
            function: Some("balancer".to_string()),
            chain: Some("forward".to_string()),
        }),
        stats: Some(balancerpb::BalancerStats {
            l4: Some(balancerpb::L4Stats {
                incoming_packets: 1_200_000,
                select_vs_failed: 100,
                invalid_packets: 350,
                select_real_failed: 50,
                outgoing_packets: 1_199_500,
            }),
            icmpv4: Some(balancerpb::IcmpStats {
                src_not_allowed: 1_215,
                incoming_packets: 34_567,
                echo_responses: 30_000,
                payload_too_short_ip: 12,
                unmatching_src_from_original: 8,
                payload_too_short_port: 5,
                unexpected_transport: 3,
                unrecognized_vs: 2,
                forwarded_packets: 4_500,
                broadcasted_packets: 67,
                packet_clones_sent: 150,
                packet_clones_received: 145,
                packet_clone_failures: 5,
            }),
            icmpv6: Some(balancerpb::IcmpStats {
                src_not_allowed: 1_215,
                incoming_packets: 1_234,
                echo_responses: 1_200,
                payload_too_short_ip: 2,
                unmatching_src_from_original: 1,
                payload_too_short_port: 1,
                unexpected_transport: 0,
                unrecognized_vs: 0,
                forwarded_packets: 34,
                broadcasted_packets: 2,
                packet_clones_sent: 10,
                packet_clones_received: 9,
                packet_clone_failures: 1,
            }),
            common: Some(balancerpb::CommonStats {
                incoming_packets: 1_234_567,
                incoming_bytes: 1_288_490_188,
                unexpected_network_proto: 0,
                decap_successful: 50_000,
                decap_failed: 123,
                outgoing_packets: 1_234_000,
                outgoing_bytes: 1_181_116_006,
            }),
            vs: vec![
                balancerpb::NamedVsStats {
                    vs: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                        port: 80,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    stats: Some(balancerpb::VsStats {
                        incoming_packets: 800_000,
                        incoming_bytes: 800_000_000,
                        packet_src_not_allowed: 0,
                        no_reals: 0,
                        ops_packets: 0,
                        session_table_overflow: 0,
                        echo_icmp_packets: 0,
                        error_icmp_packets: 0,
                        real_is_disabled: 0,
                        real_is_removed: 0,
                        not_rescheduled_packets: 0,
                        broadcasted_icmp_packets: 0,
                        created_sessions: 50_000,
                        outgoing_packets: 799_500,
                        outgoing_bytes: 799_500_000,
                    }),
                    reals: vec![
                        balancerpb::NamedRealStats {
                            real: Some(balancerpb::RealIdentifier {
                                vs: Some(balancerpb::VsIdentifier {
                                    addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                                    port: 80,
                                    proto: balancerpb::TransportProto::Tcp as i32,
                                }),
                                real: Some(balancerpb::RelativeRealIdentifier {
                                    ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 1] }),
                                    port: 80,
                                }),
                            }),
                            stats: Some(balancerpb::RealStats {
                                packets_real_disabled: 0,
                                ops_packets: 0,
                                error_icmp_packets: 0,
                                created_sessions: 30_000,
                                packets: 500_000,
                                bytes: 629_145_600,
                            }),
                        },
                        balancerpb::NamedRealStats {
                            real: Some(balancerpb::RealIdentifier {
                                vs: Some(balancerpb::VsIdentifier {
                                    addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                                    port: 80,
                                    proto: balancerpb::TransportProto::Tcp as i32,
                                }),
                                real: Some(balancerpb::RelativeRealIdentifier {
                                    ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 2] }),
                                    port: 80,
                                }),
                            }),
                            stats: Some(balancerpb::RealStats {
                                packets_real_disabled: 0,
                                ops_packets: 0,
                                error_icmp_packets: 0,
                                created_sessions: 20_000,
                                packets: 300_000,
                                bytes: 366_503_875,
                            }),
                        },
                    ],
                },
                balancerpb::NamedVsStats {
                    vs: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                        port: 443,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    stats: Some(balancerpb::VsStats {
                        incoming_packets: 400_000,
                        incoming_bytes: 400_000_000,
                        packet_src_not_allowed: 0,
                        no_reals: 0,
                        ops_packets: 0,
                        session_table_overflow: 0,
                        echo_icmp_packets: 0,
                        error_icmp_packets: 0,
                        real_is_disabled: 0,
                        real_is_removed: 0,
                        not_rescheduled_packets: 0,
                        broadcasted_icmp_packets: 0,
                        created_sessions: 25_000,
                        outgoing_packets: 400_000,
                        outgoing_bytes: 400_000_000,
                    }),
                    reals: vec![balancerpb::NamedRealStats {
                        real: Some(balancerpb::RealIdentifier {
                            vs: Some(balancerpb::VsIdentifier {
                                addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                                port: 443,
                                proto: balancerpb::TransportProto::Tcp as i32,
                            }),
                            real: Some(balancerpb::RelativeRealIdentifier {
                                ip: Some(balancerpb::Addr { bytes: vec![10, 2, 1, 1] }),
                                port: 443,
                            }),
                        }),
                        stats: Some(balancerpb::RealStats {
                            packets_real_disabled: 0,
                            ops_packets: 0,
                            error_icmp_packets: 0,
                            created_sessions: 25_000,
                            packets: 400_000,
                            bytes: 503_316_480,
                        }),
                    }],
                },
            ],
        }),
    }
}

#[allow(dead_code)]
pub fn create_sessions_info_example() -> balancerpb::ShowSessionsResponse {
    balancerpb::ShowSessionsResponse {
        name: "my-balancer".to_string(),
        sessions: vec![
            balancerpb::SessionInfo {
                client_addr: Some(balancerpb::Addr { bytes: vec![10, 0, 1, 100] }),
                client_port: 45678,
                vs_id: Some(balancerpb::VsIdentifier {
                    addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                    port: 80,
                    proto: balancerpb::TransportProto::Tcp as i32,
                }),
                real_id: Some(balancerpb::RealIdentifier {
                    vs: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                        port: 80,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    real: Some(balancerpb::RelativeRealIdentifier {
                        ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 1] }),
                        port: 80,
                    }),
                }),
                create_timestamp: Some(prost_types::Timestamp { seconds: 1705315530, nanos: 0 }),
                last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315844, nanos: 0 }),
                timeout: Some(prost_types::Duration { seconds: 60, nanos: 0 }),
            },
            balancerpb::SessionInfo {
                client_addr: Some(balancerpb::Addr { bytes: vec![10, 0, 1, 101] }),
                client_port: 45679,
                vs_id: Some(balancerpb::VsIdentifier {
                    addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                    port: 80,
                    proto: balancerpb::TransportProto::Tcp as i32,
                }),
                real_id: Some(balancerpb::RealIdentifier {
                    vs: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                        port: 80,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    real: Some(balancerpb::RelativeRealIdentifier {
                        ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 2] }),
                        port: 80,
                    }),
                }),
                create_timestamp: Some(prost_types::Timestamp { seconds: 1705315531, nanos: 0 }),
                last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315845, nanos: 0 }),
                timeout: Some(prost_types::Duration { seconds: 60, nanos: 0 }),
            },
            balancerpb::SessionInfo {
                client_addr: Some(balancerpb::Addr { bytes: vec![10, 0, 2, 50] }),
                client_port: 52000,
                vs_id: Some(balancerpb::VsIdentifier {
                    addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                    port: 443,
                    proto: balancerpb::TransportProto::Tcp as i32,
                }),
                real_id: Some(balancerpb::RealIdentifier {
                    vs: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                        port: 443,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    real: Some(balancerpb::RelativeRealIdentifier {
                        ip: Some(balancerpb::Addr { bytes: vec![10, 2, 1, 1] }),
                        port: 443,
                    }),
                }),
                create_timestamp: Some(prost_types::Timestamp { seconds: 1705315695, nanos: 0 }),
                last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315840, nanos: 0 }),
                timeout: Some(prost_types::Duration { seconds: 60, nanos: 0 }),
            },
            balancerpb::SessionInfo {
                client_addr: Some(balancerpb::Addr { bytes: vec![10, 0, 2, 51] }),
                client_port: 52001,
                vs_id: Some(balancerpb::VsIdentifier {
                    addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                    port: 443,
                    proto: balancerpb::TransportProto::Tcp as i32,
                }),
                real_id: Some(balancerpb::RealIdentifier {
                    vs: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                        port: 443,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    real: Some(balancerpb::RelativeRealIdentifier {
                        ip: Some(balancerpb::Addr { bytes: vec![10, 2, 1, 1] }),
                        port: 443,
                    }),
                }),
                create_timestamp: Some(prost_types::Timestamp { seconds: 1705315696, nanos: 0 }),
                last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315841, nanos: 0 }),
                timeout: Some(prost_types::Duration { seconds: 60, nanos: 0 }),
            },
            balancerpb::SessionInfo {
                client_addr: Some(balancerpb::Addr { bytes: vec![10, 0, 2, 52] }),
                client_port: 52002,
                vs_id: Some(balancerpb::VsIdentifier {
                    addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                    port: 443,
                    proto: balancerpb::TransportProto::Tcp as i32,
                }),
                real_id: Some(balancerpb::RealIdentifier {
                    vs: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                        port: 443,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    real: Some(balancerpb::RelativeRealIdentifier {
                        ip: Some(balancerpb::Addr { bytes: vec![10, 2, 1, 2] }),
                        port: 443,
                    }),
                }),
                create_timestamp: Some(prost_types::Timestamp { seconds: 1705315697, nanos: 0 }),
                last_packet_timestamp: Some(prost_types::Timestamp { seconds: 1705315842, nanos: 0 }),
                timeout: Some(prost_types::Duration { seconds: 60, nanos: 0 }),
            },
        ],
    }
}
