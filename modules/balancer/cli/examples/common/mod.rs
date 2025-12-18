//! Common data creation functions for examples

use yanet_cli_balancer::rpc::balancerpb;
use yanet_cli_balancer::rpc::commonpb;

#[allow(dead_code)]
pub fn create_show_config_example() -> balancerpb::ShowConfigResponse {
    balancerpb::ShowConfigResponse {
        target: Some(commonpb::TargetModule {
            config_name: "my-balancer".to_string(),
        }),
        module_config: Some(balancerpb::ModuleConfig {
            virtual_services: vec![
                balancerpb::VirtualService {
                    addr: vec![192, 0, 2, 1],
                    port: 80,
                    proto: balancerpb::TransportProto::Tcp as i32,
                    scheduler: balancerpb::VsScheduler::Wrr as i32,
                    allowed_srcs: vec![
                        balancerpb::Subnet {
                            addr: vec![10, 0, 0, 0],
                            size: 8,
                        },
                        balancerpb::Subnet {
                            addr: vec![172, 16, 0, 0],
                            size: 12,
                        },
                    ],
                    reals: vec![
                        balancerpb::Real {
                            weight: 100,
                            dst_addr: vec![10, 1, 1, 1],
                            src_addr: vec![192, 0, 2, 1],
                            src_mask: vec![255, 255, 255, 255],
                            enabled: true,
                            port: 80,
                        },
                        balancerpb::Real {
                            weight: 50,
                            dst_addr: vec![10, 1, 1, 2],
                            src_addr: vec![192, 0, 2, 1],
                            src_mask: vec![255, 255, 255, 255],
                            enabled: true,
                            port: 80,
                        },
                    ],
                    flags: Some(balancerpb::VsFlags {
                        gre: true,
                        fix_mss: true,
                        ops: true,
                        pure_l3: false,
                    }),
                    peers: vec![vec![192, 0, 2, 10], vec![192, 0, 2, 11]],
                },
                balancerpb::VirtualService {
                    addr: vec![192, 0, 2, 2],
                    port: 443,
                    proto: balancerpb::TransportProto::Tcp as i32,
                    scheduler: balancerpb::VsScheduler::Wlc as i32,
                    allowed_srcs: vec![balancerpb::Subnet {
                        addr: vec![0, 0, 0, 0],
                        size: 0,
                    }],
                    reals: vec![
                        balancerpb::Real {
                            weight: 100,
                            dst_addr: vec![10, 2, 1, 1],
                            src_addr: vec![192, 0, 2, 2],
                            src_mask: vec![255, 255, 255, 255],
                            enabled: true,
                            port: 443,
                        },
                        balancerpb::Real {
                            weight: 100,
                            dst_addr: vec![10, 2, 1, 2],
                            src_addr: vec![192, 0, 2, 2],
                            src_mask: vec![255, 255, 255, 255],
                            enabled: true,
                            port: 443,
                        },
                        balancerpb::Real {
                            weight: 50,
                            dst_addr: vec![10, 2, 1, 3],
                            src_addr: vec![192, 0, 2, 2],
                            src_mask: vec![255, 255, 255, 255],
                            enabled: false,
                            port: 443,
                        },
                    ],
                    flags: Some(balancerpb::VsFlags {
                        gre: false,
                        fix_mss: true,
                        ops: true,
                        pure_l3: true,
                    }),
                    peers: vec![vec![192, 0, 2, 10]],
                },
            ],
            source_address_v4: vec![192, 0, 2, 1],
            source_address_v6: vec![0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1],
            decap_addresses: vec![
                vec![192, 0, 2, 1],
                vec![0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1],
            ],
            sessions_timeouts: Some(balancerpb::SessionsTimeouts {
                tcp_syn_ack: 10,
                tcp_syn: 10,
                tcp_fin: 10,
                tcp: 60,
                udp: 30,
                default: 60,
            }),
            wlc: Some(balancerpb::WlcConfig {
                wlc_power: 10,
                max_real_weight: 1000,
                update_period: Some(prost_types::Duration {
                    seconds: 5,
                    nanos: 0,
                }),
            }),
        }),
        module_state_config: Some(balancerpb::ModuleStateConfig {
            session_table_capacity: 1_000_000,
            session_table_scan_period: Some(prost_types::Duration {
                seconds: 1,
                nanos: 0,
            }),
            session_table_max_load_factor: 0.75,
        }),
        buffered_real_updates: vec![
            balancerpb::RealUpdate {
                virtual_ip: vec![192, 0, 2, 1],
                port: 80,
                proto: balancerpb::TransportProto::Tcp as i32,
                real_ip: vec![10, 1, 1, 3],
                enable: true,
                weight: 75,
            },
        ],
    }
}

#[allow(dead_code)]
pub fn create_list_configs_example() -> balancerpb::ListConfigsResponse {
    // First config - full production-like config
    let config1 = create_show_config_example();
    
    // Second config - test environment with different settings
    let config2 = balancerpb::ShowConfigResponse {
        target: Some(commonpb::TargetModule {
            config_name: "test-balancer".to_string(),
        }),
        module_config: Some(balancerpb::ModuleConfig {
            virtual_services: vec![
                balancerpb::VirtualService {
                    addr: vec![198, 51, 100, 1],
                    port: 8080,
                    proto: balancerpb::TransportProto::Tcp as i32,
                    scheduler: balancerpb::VsScheduler::Prr as i32,
                    allowed_srcs: vec![],
                    reals: vec![
                        balancerpb::Real {
                            weight: 100,
                            dst_addr: vec![10, 10, 1, 1],
                            src_addr: vec![198, 51, 100, 1],
                            src_mask: vec![255, 255, 255, 255],
                            enabled: true,
                            port: 8080,
                        },
                        balancerpb::Real {
                            weight: 100,
                            dst_addr: vec![10, 10, 1, 2],
                            src_addr: vec![198, 51, 100, 1],
                            src_mask: vec![255, 255, 255, 255],
                            enabled: false,
                            port: 8080,
                        },
                    ],
                    flags: Some(balancerpb::VsFlags {
                        gre: true,
                        fix_mss: true,
                        ops: false,
                        pure_l3: true,
                    }),
                    peers: vec![],
                },
            ],
            source_address_v4: vec![198, 51, 100, 1],
            source_address_v6: vec![0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2],
            decap_addresses: vec![vec![198, 51, 100, 1]],
            sessions_timeouts: Some(balancerpb::SessionsTimeouts {
                tcp_syn_ack: 5,
                tcp_syn: 5,
                tcp_fin: 5,
                tcp: 30,
                udp: 15,
                default: 30,
            }),
            wlc: Some(balancerpb::WlcConfig {
                wlc_power: 5,
                max_real_weight: 500,
                update_period: Some(prost_types::Duration {
                    seconds: 10,
                    nanos: 0,
                }),
            }),
        }),
        module_state_config: Some(balancerpb::ModuleStateConfig {
            session_table_capacity: 500_000,
            session_table_scan_period: Some(prost_types::Duration {
                seconds: 2,
                nanos: 0,
            }),
            session_table_max_load_factor: 0.8,
        }),
        buffered_real_updates: vec![
            balancerpb::RealUpdate {
                virtual_ip: vec![198, 51, 100, 1],
                port: 8080,
                proto: balancerpb::TransportProto::Tcp as i32,
                real_ip: vec![10, 10, 1, 2],
                enable: true,
                weight: 150,
            },
            balancerpb::RealUpdate {
                virtual_ip: vec![198, 51, 100, 1],
                port: 8080,
                proto: balancerpb::TransportProto::Tcp as i32,
                real_ip: vec![10, 10, 1, 3],
                enable: true,
                weight: 100,
            },
        ],
    };
    
    balancerpb::ListConfigsResponse {
        configs: vec![config1, config2],
    }
}

#[allow(dead_code)]
pub fn create_state_info_example() -> balancerpb::StateInfoResponse {
    balancerpb::StateInfoResponse {
        target: Some(commonpb::TargetModule {
            config_name: "my-balancer".to_string(),
        }),
        info: Some(balancerpb::BalancerInfo {
            active_sessions: Some(balancerpb::AsyncInfo {
                value: 15234,
                updated_at: Some(prost_types::Timestamp {
                    seconds: 1705315845,
                    nanos: 0,
                }),
            }),
            module: Some(balancerpb::ModuleStats {
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
                    payload_too_short_ip: 0,
                    unmatching_src_from_original: 0,
                    payload_too_short_port: 0,
                    unexpected_transport: 0,
                    unrecognized_vs: 0,
                    forwarded_packets: 4_500,
                    broadcasted_packets: 67,
                    packet_clones_sent: 0,
                    packet_clones_received: 0,
                    packet_clone_failures: 0,
                }),
                icmpv6: Some(balancerpb::IcmpStats {
                    src_not_allowed: 1_215,
                    incoming_packets: 1_234,
                    echo_responses: 1_200,
                    payload_too_short_ip: 0,
                    unmatching_src_from_original: 0,
                    payload_too_short_port: 0,
                    unexpected_transport: 0,
                    unrecognized_vs: 0,
                    forwarded_packets: 34,
                    broadcasted_packets: 0,
                    packet_clones_sent: 0,
                    packet_clones_received: 0,
                    packet_clone_failures: 0,
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
            }),
            vs_info: vec![
                balancerpb::VsInfo {
                    vs_registry_idx: 0,
                    vs_ip: vec![192, 0, 2, 1],
                    vs_port: 80,
                    vs_proto: balancerpb::TransportProto::Tcp as i32,
                    active_sessions: Some(balancerpb::AsyncInfo {
                        value: 10234,
                        updated_at: Some(prost_types::Timestamp {
                            seconds: 1705315844,
                            nanos: 0,
                        }),
                    }),
                    last_packet_timestamp: Some(prost_types::Timestamp {
                        seconds: 1705315844,
                        nanos: 0,
                    }),
                    stats: Some(balancerpb::VsStats {
                        incoming_packets: 800_000,
                        incoming_bytes: 800_000_000,
                        packet_src_not_allowed: 100,
                        no_reals: 50,
                        ops_packets: 1_000,
                        session_table_overflow: 25,
                        echo_icmp_packets: 500,
                        error_icmp_packets: 200,
                        real_is_disabled: 150,
                        real_is_removed: 75,
                        not_rescheduled_packets: 300,
                        broadcasted_icmp_packets: 400,
                        created_sessions: 50_000,
                        outgoing_packets: 799_500,
                        outgoing_bytes: 799_500_000,
                    }),
                },
                balancerpb::VsInfo {
                    vs_registry_idx: 1,
                    vs_ip: vec![192, 0, 2, 2],
                    vs_port: 443,
                    vs_proto: balancerpb::TransportProto::Tcp as i32,
                    active_sessions: Some(balancerpb::AsyncInfo {
                        value: 5000,
                        updated_at: Some(prost_types::Timestamp {
                            seconds: 1705315845,
                            nanos: 0,
                        }),
                    }),
                    last_packet_timestamp: Some(prost_types::Timestamp {
                        seconds: 1705315845,
                        nanos: 0,
                    }),
                    stats: Some(balancerpb::VsStats {
                        incoming_packets: 400_000,
                        incoming_bytes: 400_000_000,
                        packet_src_not_allowed: 50,
                        no_reals: 25,
                        ops_packets: 500,
                        session_table_overflow: 10,
                        echo_icmp_packets: 250,
                        error_icmp_packets: 100,
                        real_is_disabled: 75,
                        real_is_removed: 40,
                        not_rescheduled_packets: 150,
                        broadcasted_icmp_packets: 200,
                        created_sessions: 25_000,
                        outgoing_packets: 400_000,
                        outgoing_bytes: 400_000_000,
                    }),
                },
            ],
            real_info: vec![
                balancerpb::RealInfo {
                    real_registry_idx: 0,
                    vs_ip: vec![192, 0, 2, 1],
                    vs_port: 80,
                    vs_proto: balancerpb::TransportProto::Tcp as i32,
                    real_ip: vec![10, 1, 1, 1],
                    active_sessions: Some(balancerpb::AsyncInfo {
                        value: 6000,
                        updated_at: Some(prost_types::Timestamp {
                            seconds: 1705315843,
                            nanos: 0,
                        }),
                    }),
                    last_packet_timestamp: Some(prost_types::Timestamp {
                        seconds: 1705315844,
                        nanos: 0,
                    }),
                    stats: Some(balancerpb::RealStats {
                        packets_real_disabled: 50,
                        packets_real_not_present: 25,
                        ops_packets: 500,
                        error_icmp_packets: 100,
                        created_sessions: 30_000,
                        packets: 500_000,
                        bytes: 629_145_600,
                    }),
                },
                balancerpb::RealInfo {
                    real_registry_idx: 1,
                    vs_ip: vec![192, 0, 2, 1],
                    vs_port: 80,
                    vs_proto: balancerpb::TransportProto::Tcp as i32,
                    real_ip: vec![10, 1, 1, 2],
                    active_sessions: Some(balancerpb::AsyncInfo {
                        value: 4234,
                        updated_at: Some(prost_types::Timestamp {
                            seconds: 1705315844,
                            nanos: 0,
                        }),
                    }),
                    last_packet_timestamp: Some(prost_types::Timestamp {
                        seconds: 1705315843,
                        nanos: 0,
                    }),
                    stats: Some(balancerpb::RealStats {
                        packets_real_disabled: 30,
                        packets_real_not_present: 15,
                        ops_packets: 300,
                        error_icmp_packets: 60,
                        created_sessions: 20_000,
                        packets: 300_000,
                        bytes: 366_503_875,
                    }),
                },
                balancerpb::RealInfo {
                    real_registry_idx: 2,
                    vs_ip: vec![192, 0, 2, 2],
                    vs_port: 443,
                    vs_proto: balancerpb::TransportProto::Tcp as i32,
                    real_ip: vec![10, 2, 1, 1],
                    active_sessions: Some(balancerpb::AsyncInfo {
                        value: 5000,
                        updated_at: Some(prost_types::Timestamp {
                            seconds: 1705315845,
                            nanos: 0,
                        }),
                    }),
                    last_packet_timestamp: Some(prost_types::Timestamp {
                        seconds: 1705315845,
                        nanos: 0,
                    }),
                    stats: Some(balancerpb::RealStats {
                        packets_real_disabled: 40,
                        packets_real_not_present: 20,
                        ops_packets: 400,
                        error_icmp_packets: 80,
                        created_sessions: 25_000,
                        packets: 400_000,
                        bytes: 503_316_480,
                    }),
                },
            ],
        }),
    }
}

#[allow(dead_code)]
pub fn create_config_stats_example() -> balancerpb::ConfigStatsResponse {
    balancerpb::ConfigStatsResponse {
        target: Some(commonpb::TargetModule {
            config_name: "my-balancer".to_string(),
        }),
        device: "eth0".to_string(),
        pipeline: "main".to_string(),
        function: "balancer".to_string(),
        chain: "forward".to_string(),
        stats: Some(balancerpb::BalancerStats {
            module: Some(balancerpb::ModuleStats {
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
            }),
            vs: vec![
                balancerpb::VsStatsInfo {
                    vs_registry_idx: 0,
                    ip: vec![192, 0, 2, 1],
                    port: 80,
                    proto: balancerpb::TransportProto::Tcp as i32,
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
                },
                balancerpb::VsStatsInfo {
                    vs_registry_idx: 1,
                    ip: vec![192, 0, 2, 2],
                    port: 443,
                    proto: balancerpb::TransportProto::Tcp as i32,
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
                },
            ],
            reals: vec![
                balancerpb::RealStatsInfo {
                    real_registry_idx: 0,
                    vs_ip: vec![192, 0, 2, 1],
                    port: 80,
                    proto: balancerpb::TransportProto::Tcp as i32,
                    real_ip: vec![10, 1, 1, 1],
                    stats: Some(balancerpb::RealStats {
                        packets_real_disabled: 0,
                        packets_real_not_present: 0,
                        ops_packets: 0,
                        error_icmp_packets: 0,
                        created_sessions: 30_000,
                        packets: 500_000,
                        bytes: 629_145_600,
                    }),
                },
                balancerpb::RealStatsInfo {
                    real_registry_idx: 1,
                    vs_ip: vec![192, 0, 2, 1],
                    port: 80,
                    proto: balancerpb::TransportProto::Tcp as i32,
                    real_ip: vec![10, 1, 1, 2],
                    stats: Some(balancerpb::RealStats {
                        packets_real_disabled: 0,
                        packets_real_not_present: 0,
                        ops_packets: 0,
                        error_icmp_packets: 0,
                        created_sessions: 20_000,
                        packets: 300_000,
                        bytes: 366_503_875,
                    }),
                },
                balancerpb::RealStatsInfo {
                    real_registry_idx: 2,
                    vs_ip: vec![192, 0, 2, 2],
                    port: 443,
                    proto: balancerpb::TransportProto::Tcp as i32,
                    real_ip: vec![10, 2, 1, 1],
                    stats: Some(balancerpb::RealStats {
                        packets_real_disabled: 0,
                        packets_real_not_present: 0,
                        ops_packets: 0,
                        error_icmp_packets: 0,
                        created_sessions: 25_000,
                        packets: 400_000,
                        bytes: 503_316_480,
                    }),
                },
            ],
        }),
    }
}

#[allow(dead_code)]
pub fn create_sessions_info_example() -> balancerpb::SessionsInfoResponse {
    balancerpb::SessionsInfoResponse {
        target: Some(commonpb::TargetModule {
            config_name: "my-balancer".to_string(),
        }),
        sessions_info: vec![
            balancerpb::SessionInfo {
                client_addr: vec![10, 0, 1, 100],
                client_port: 45678,
                vs_addr: vec![192, 0, 2, 1],
                vs_port: 80,
                real_addr: vec![10, 1, 1, 1],
                real_port: 80,
                create_timestamp: Some(prost_types::Timestamp {
                    seconds: 1705315530,
                    nanos: 0,
                }),
                last_packet_timestamp: Some(prost_types::Timestamp {
                    seconds: 1705315844,
                    nanos: 0,
                }),
                timeout: Some(prost_types::Duration {
                    seconds: 60,
                    nanos: 0,
                }),
            },
            balancerpb::SessionInfo {
                client_addr: vec![10, 0, 1, 101],
                client_port: 45679,
                vs_addr: vec![192, 0, 2, 1],
                vs_port: 80,
                real_addr: vec![10, 1, 1, 2],
                real_port: 80,
                create_timestamp: Some(prost_types::Timestamp {
                    seconds: 1705315531,
                    nanos: 0,
                }),
                last_packet_timestamp: Some(prost_types::Timestamp {
                    seconds: 1705315845,
                    nanos: 0,
                }),
                timeout: Some(prost_types::Duration {
                    seconds: 60,
                    nanos: 0,
                }),
            },
            balancerpb::SessionInfo {
                client_addr: vec![10, 0, 2, 50],
                client_port: 52000,
                vs_addr: vec![192, 0, 2, 2],
                vs_port: 443,
                real_addr: vec![10, 2, 1, 1],
                real_port: 443,
                create_timestamp: Some(prost_types::Timestamp {
                    seconds: 1705315695,
                    nanos: 0,
                }),
                last_packet_timestamp: Some(prost_types::Timestamp {
                    seconds: 1705315840,
                    nanos: 0,
                }),
                timeout: Some(prost_types::Duration {
                    seconds: 60,
                    nanos: 0,
                }),
            },
            balancerpb::SessionInfo {
                client_addr: vec![10, 0, 2, 51],
                client_port: 52001,
                vs_addr: vec![192, 0, 2, 2],
                vs_port: 443,
                real_addr: vec![10, 2, 1, 1],
                real_port: 443,
                create_timestamp: Some(prost_types::Timestamp {
                    seconds: 1705315696,
                    nanos: 0,
                }),
                last_packet_timestamp: Some(prost_types::Timestamp {
                    seconds: 1705315841,
                    nanos: 0,
                }),
                timeout: Some(prost_types::Duration {
                    seconds: 60,
                    nanos: 0,
                }),
            },
            balancerpb::SessionInfo {
                client_addr: vec![10, 0, 2, 52],
                client_port: 52002,
                vs_addr: vec![192, 0, 2, 2],
                vs_port: 443,
                real_addr: vec![10, 2, 1, 2],
                real_port: 443,
                create_timestamp: Some(prost_types::Timestamp {
                    seconds: 1705315697,
                    nanos: 0,
                }),
                last_packet_timestamp: Some(prost_types::Timestamp {
                    seconds: 1705315842,
                    nanos: 0,
                }),
                timeout: Some(prost_types::Duration {
                    seconds: 60,
                    nanos: 0,
                }),
            },
        ],
    }
}