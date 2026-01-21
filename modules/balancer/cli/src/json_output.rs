//! JSON output with proper IP address formatting

use serde::Serialize;

use crate::{
    entities::{addr_to_ip, opt_addr_to_ip},
    rpc::balancerpb,
};

////////////////////////////////////////////////////////////////////////////////
// ShowConfig JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct ShowConfigJson {
    pub config: Option<BalancerConfigJson>,
    pub buffered_real_updates: Vec<RealUpdateJson>,
}

#[derive(Serialize)]
pub struct BalancerConfigJson {
    pub packet_handler: Option<PacketHandlerConfigJson>,
    pub state: Option<StateConfigJson>,
}

#[derive(Serialize)]
pub struct PacketHandlerConfigJson {
    pub virtual_services: Vec<VirtualServiceJson>,
    pub source_address_v4: String,
    pub source_address_v6: String,
    pub decap_addresses: Vec<String>,
    pub sessions_timeouts: Option<SessionsTimeoutsJson>,
}

#[derive(Serialize)]
pub struct VirtualServiceJson {
    pub id: Option<VsIdentifierJson>,
    pub scheduler: String,
    pub allowed_srcs: Vec<SubnetJson>,
    pub reals: Vec<RealJson>,
    pub flags: Option<VsFlagsJson>,
    pub peers: Vec<String>,
}

#[derive(Serialize)]
pub struct VsIdentifierJson {
    pub addr: String,
    pub port: u32,
    pub proto: String,
}

#[derive(Serialize)]
pub struct SubnetJson {
    pub addr: String,
    pub size: u32,
}

#[derive(Serialize)]
pub struct RealJson {
    pub id: Option<RelativeRealIdentifierJson>,
    pub weight: u32,
    pub src_addr: String,
    pub src_mask: String,
}

#[derive(Serialize)]
pub struct RelativeRealIdentifierJson {
    pub ip: String,
    pub port: u32,
}

#[derive(Serialize)]
pub struct VsFlagsJson {
    pub gre: bool,
    pub fix_mss: bool,
    pub ops: bool,
    pub pure_l3: bool,
    pub wlc: bool,
}

#[derive(Serialize)]
pub struct SessionsTimeoutsJson {
    pub tcp_syn_ack: u32,
    pub tcp_syn: u32,
    pub tcp_fin: u32,
    pub tcp: u32,
    pub udp: u32,
    pub default: u32,
}

#[derive(Serialize)]
pub struct StateConfigJson {
    pub session_table_capacity: Option<u64>,
    pub session_table_max_load_factor: Option<f32>,
    pub wlc: Option<WlcConfigJson>,
    pub refresh_period: Option<String>,
}

#[derive(Serialize)]
pub struct WlcConfigJson {
    pub power: Option<u64>,
    pub max_weight: Option<u32>,
}

#[derive(Serialize)]
pub struct RealUpdateJson {
    pub real_id: Option<RealIdentifierJson>,
    pub enable: Option<bool>,
    pub weight: Option<u32>,
}

#[derive(Serialize)]
pub struct RealIdentifierJson {
    pub vs: Option<VsIdentifierJson>,
    pub real: Option<RelativeRealIdentifierJson>,
}

////////////////////////////////////////////////////////////////////////////////
// ShowInfo JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct ShowInfoResponseJson {
    pub name: String,
    pub info: Option<BalancerInfoJson>,
}

#[derive(Serialize)]
pub struct BalancerInfoJson {
    pub active_sessions: u64,
    pub last_packet_timestamp: Option<String>,
    pub vs: Vec<VsInfoJson>,
}

#[derive(Serialize)]
pub struct VsInfoJson {
    pub id: Option<VsIdentifierJson>,
    pub active_sessions: u64,
    pub last_packet_timestamp: Option<String>,
    pub reals: Vec<RealInfoJson>,
}

#[derive(Serialize)]
pub struct RealInfoJson {
    pub id: Option<RealIdentifierJson>,
    pub active_sessions: u64,
    pub last_packet_timestamp: Option<String>,
}

////////////////////////////////////////////////////////////////////////////////
// ShowStats JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct ShowStatsResponseJson {
    pub ref_: Option<PacketHandlerRefJson>,
    pub stats: Option<BalancerStatsJson>,
}

#[derive(Serialize)]
pub struct PacketHandlerRefJson {
    pub device: Option<String>,
    pub pipeline: Option<String>,
    pub function: Option<String>,
    pub chain: Option<String>,
}

#[derive(Serialize)]
pub struct BalancerStatsJson {
    pub l4: Option<L4StatsJson>,
    pub icmpv4: Option<IcmpStatsJson>,
    pub icmpv6: Option<IcmpStatsJson>,
    pub common: Option<CommonStatsJson>,
    pub vs: Vec<NamedVsStatsJson>,
}

#[derive(Serialize)]
pub struct L4StatsJson {
    pub incoming_packets: u64,
    pub select_vs_failed: u64,
    pub invalid_packets: u64,
    pub select_real_failed: u64,
    pub outgoing_packets: u64,
}

#[derive(Serialize)]
pub struct IcmpStatsJson {
    pub incoming_packets: u64,
    pub src_not_allowed: u64,
    pub echo_responses: u64,
    pub payload_too_short_ip: u64,
    pub unmatching_src_from_original: u64,
    pub payload_too_short_port: u64,
    pub unexpected_transport: u64,
    pub unrecognized_vs: u64,
    pub forwarded_packets: u64,
    pub broadcasted_packets: u64,
    pub packet_clones_sent: u64,
    pub packet_clones_received: u64,
    pub packet_clone_failures: u64,
}

#[derive(Serialize)]
pub struct CommonStatsJson {
    pub incoming_packets: u64,
    pub incoming_bytes: u64,
    pub unexpected_network_proto: u64,
    pub decap_successful: u64,
    pub decap_failed: u64,
    pub outgoing_packets: u64,
    pub outgoing_bytes: u64,
}

#[derive(Serialize)]
pub struct NamedVsStatsJson {
    pub vs: Option<VsIdentifierJson>,
    pub stats: Option<VsStatsJson>,
    pub reals: Vec<NamedRealStatsJson>,
}

#[derive(Serialize)]
pub struct VsStatsJson {
    pub incoming_packets: u64,
    pub incoming_bytes: u64,
    pub packet_src_not_allowed: u64,
    pub no_reals: u64,
    pub ops_packets: u64,
    pub session_table_overflow: u64,
    pub echo_icmp_packets: u64,
    pub error_icmp_packets: u64,
    pub real_is_disabled: u64,
    pub real_is_removed: u64,
    pub not_rescheduled_packets: u64,
    pub broadcasted_icmp_packets: u64,
    pub created_sessions: u64,
    pub outgoing_packets: u64,
    pub outgoing_bytes: u64,
}

#[derive(Serialize)]
pub struct NamedRealStatsJson {
    pub real: Option<RealIdentifierJson>,
    pub stats: Option<RealStatsJson>,
}

#[derive(Serialize)]
pub struct RealStatsJson {
    pub packets_real_disabled: u64,
    pub packets_real_not_present: u64,
    pub ops_packets: u64,
    pub error_icmp_packets: u64,
    pub created_sessions: u64,
    pub packets: u64,
    pub bytes: u64,
}

////////////////////////////////////////////////////////////////////////////////
// ShowSessions JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct ShowSessionsResponseJson {
    pub sessions: Vec<SessionInfoJson>,
}

#[derive(Serialize)]
pub struct SessionInfoJson {
    pub client_addr: String,
    pub client_port: u32,
    pub vs_id: Option<VsIdentifierJson>,
    pub real_id: Option<RealIdentifierJson>,
    pub create_timestamp: Option<String>,
    pub last_packet_timestamp: Option<String>,
    pub timeout: Option<String>,
}

////////////////////////////////////////////////////////////////////////////////
// ListConfigs JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct ListConfigsResponseJson {
    pub configs: Vec<String>,
}

////////////////////////////////////////////////////////////////////////////////
// ShowGraph JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct ShowGraphResponseJson {
    pub graph: Option<GraphJson>,
}

#[derive(Serialize)]
pub struct GraphJson {
    pub virtual_services: Vec<GraphVsJson>,
}

#[derive(Serialize)]
pub struct GraphVsJson {
    pub identifier: Option<VsIdentifierJson>,
    pub reals: Vec<GraphRealJson>,
}

#[derive(Serialize)]
pub struct GraphRealJson {
    pub identifier: Option<RelativeRealIdentifierJson>,
    pub weight: u32,
    pub effective_weight: u32,
    pub enabled: bool,
}

////////////////////////////////////////////////////////////////////////////////
// Conversion functions
////////////////////////////////////////////////////////////////////////////////

fn proto_to_string(proto: i32) -> String {
    match balancerpb::TransportProto::try_from(proto) {
        Ok(balancerpb::TransportProto::Tcp) => "tcp".to_string(),
        Ok(balancerpb::TransportProto::Udp) => "udp".to_string(),
        _ => format!("Unknown({})", proto),
    }
}

fn scheduler_to_string(sched: i32) -> String {
    match balancerpb::VsScheduler::try_from(sched) {
        Ok(balancerpb::VsScheduler::SourceHash) => "source_hash".to_string(),
        Ok(balancerpb::VsScheduler::RoundRobin) => "round_robin".to_string(),
        _ => format!("Unknown({})", sched),
    }
}

fn format_timestamp(ts: Option<&prost_types::Timestamp>) -> Option<String> {
    ts.and_then(|t| {
        if t.seconds == 0 && t.nanos == 0 {
            return None;
        }
        use chrono::{DateTime, Utc};
        DateTime::<Utc>::from_timestamp(t.seconds, t.nanos as u32).map(|dt| dt.to_rfc3339())
    })
}

fn format_duration(dur: Option<&prost_types::Duration>) -> Option<String> {
    dur.map(|d| format!("{}s", d.seconds))
}

fn convert_vs_identifier(id: Option<&balancerpb::VsIdentifier>) -> Option<VsIdentifierJson> {
    id.map(|id| VsIdentifierJson {
        addr: opt_addr_to_ip(&id.addr).map(|ip| ip.to_string()).unwrap_or_default(),
        port: id.port,
        proto: proto_to_string(id.proto),
    })
}

fn convert_relative_real_identifier(
    id: Option<&balancerpb::RelativeRealIdentifier>,
) -> Option<RelativeRealIdentifierJson> {
    id.map(|id| RelativeRealIdentifierJson {
        ip: opt_addr_to_ip(&id.ip).map(|ip| ip.to_string()).unwrap_or_default(),
        port: id.port,
    })
}

fn convert_real_identifier(id: Option<&balancerpb::RealIdentifier>) -> Option<RealIdentifierJson> {
    id.map(|id| RealIdentifierJson {
        vs: convert_vs_identifier(id.vs.as_ref()),
        real: convert_relative_real_identifier(id.real.as_ref()),
    })
}

pub fn convert_show_config(response: &balancerpb::ShowConfigResponse) -> ShowConfigJson {
    ShowConfigJson {
        config: response.config.as_ref().map(|c| BalancerConfigJson {
            packet_handler: c.packet_handler.as_ref().map(|ph| PacketHandlerConfigJson {
                virtual_services: ph
                    .vs
                    .iter()
                    .map(|vs| VirtualServiceJson {
                        id: convert_vs_identifier(vs.id.as_ref()),
                        scheduler: scheduler_to_string(vs.scheduler),
                        allowed_srcs: vs
                            .allowed_srcs
                            .iter()
                            .map(|s| SubnetJson {
                                addr: opt_addr_to_ip(&s.addr).map(|ip| ip.to_string()).unwrap_or_default(),
                                size: s.size,
                            })
                            .collect(),
                        reals: vs
                            .reals
                            .iter()
                            .map(|r| RealJson {
                                id: convert_relative_real_identifier(r.id.as_ref()),
                                weight: r.weight,
                                src_addr: opt_addr_to_ip(&r.src_addr).map(|ip| ip.to_string()).unwrap_or_default(),
                                src_mask: opt_addr_to_ip(&r.src_mask).map(|ip| ip.to_string()).unwrap_or_default(),
                            })
                            .collect(),
                        flags: vs.flags.as_ref().map(|f| VsFlagsJson {
                            gre: f.gre,
                            fix_mss: f.fix_mss,
                            ops: f.ops,
                            pure_l3: f.pure_l3,
                            wlc: f.wlc,
                        }),
                        peers: vs
                            .peers
                            .iter()
                            .filter_map(|p| addr_to_ip(p).ok().map(|ip| ip.to_string()))
                            .collect(),
                    })
                    .collect(),
                source_address_v4: opt_addr_to_ip(&ph.source_address_v4)
                    .map(|ip| ip.to_string())
                    .unwrap_or_default(),
                source_address_v6: opt_addr_to_ip(&ph.source_address_v6)
                    .map(|ip| ip.to_string())
                    .unwrap_or_default(),
                decap_addresses: ph
                    .decap_addresses
                    .iter()
                    .filter_map(|a| addr_to_ip(a).ok().map(|ip| ip.to_string()))
                    .collect(),
                sessions_timeouts: ph.sessions_timeouts.as_ref().map(|t| SessionsTimeoutsJson {
                    tcp_syn_ack: t.tcp_syn_ack,
                    tcp_syn: t.tcp_syn,
                    tcp_fin: t.tcp_fin,
                    tcp: t.tcp,
                    udp: t.udp,
                    default: t.default,
                }),
            }),
            state: c.state.as_ref().map(|s| StateConfigJson {
                session_table_capacity: s.session_table_capacity,
                session_table_max_load_factor: s.session_table_max_load_factor,
                wlc: s.wlc.as_ref().map(|w| WlcConfigJson {
                    power: w.power,
                    max_weight: w.max_weight,
                }),
                refresh_period: s
                    .refresh_period
                    .as_ref()
                    .map(|p| format!("{}ms", p.seconds * 1000 + p.nanos as i64 / 1_000_000)),
            }),
        }),
        buffered_real_updates: response
            .buffered_real_updates
            .iter()
            .map(|u| RealUpdateJson {
                real_id: convert_real_identifier(u.real_id.as_ref()),
                enable: u.enable,
                weight: u.weight,
            })
            .collect(),
    }
}

pub fn convert_list_configs(response: &balancerpb::ListConfigsResponse) -> ListConfigsResponseJson {
    ListConfigsResponseJson { configs: response.configs.clone() }
}

pub fn convert_show_info(response: &balancerpb::ShowInfoResponse) -> ShowInfoResponseJson {
    ShowInfoResponseJson {
        name: response.name.clone(),
        info: response.info.as_ref().map(|i| BalancerInfoJson {
            active_sessions: i.active_sessions,
            last_packet_timestamp: format_timestamp(i.last_packet_timestamp.as_ref()),
            vs: i
                .vs
                .iter()
                .map(|v| VsInfoJson {
                    id: convert_vs_identifier(v.id.as_ref()),
                    active_sessions: v.active_sessions,
                    last_packet_timestamp: format_timestamp(v.last_packet_timestamp.as_ref()),
                    reals: v
                        .reals
                        .iter()
                        .map(|r| RealInfoJson {
                            id: convert_real_identifier(r.id.as_ref()),
                            active_sessions: r.active_sessions,
                            last_packet_timestamp: format_timestamp(r.last_packet_timestamp.as_ref()),
                        })
                        .collect(),
                })
                .collect(),
        }),
    }
}

pub fn convert_show_stats(response: &balancerpb::ShowStatsResponse) -> ShowStatsResponseJson {
    ShowStatsResponseJson {
        ref_: response.r#ref.as_ref().map(|r| PacketHandlerRefJson {
            device: r.device.clone(),
            pipeline: r.pipeline.clone(),
            function: r.function.clone(),
            chain: r.chain.clone(),
        }),
        stats: response.stats.as_ref().map(|s| BalancerStatsJson {
            l4: s.l4.as_ref().map(|l| L4StatsJson {
                incoming_packets: l.incoming_packets,
                select_vs_failed: l.select_vs_failed,
                invalid_packets: l.invalid_packets,
                select_real_failed: l.select_real_failed,
                outgoing_packets: l.outgoing_packets,
            }),
            icmpv4: s.icmpv4.as_ref().map(|i| IcmpStatsJson {
                incoming_packets: i.incoming_packets,
                src_not_allowed: i.src_not_allowed,
                echo_responses: i.echo_responses,
                payload_too_short_ip: i.payload_too_short_ip,
                unmatching_src_from_original: i.unmatching_src_from_original,
                payload_too_short_port: i.payload_too_short_port,
                unexpected_transport: i.unexpected_transport,
                unrecognized_vs: i.unrecognized_vs,
                forwarded_packets: i.forwarded_packets,
                broadcasted_packets: i.broadcasted_packets,
                packet_clones_sent: i.packet_clones_sent,
                packet_clones_received: i.packet_clones_received,
                packet_clone_failures: i.packet_clone_failures,
            }),
            icmpv6: s.icmpv6.as_ref().map(|i| IcmpStatsJson {
                incoming_packets: i.incoming_packets,
                src_not_allowed: i.src_not_allowed,
                echo_responses: i.echo_responses,
                payload_too_short_ip: i.payload_too_short_ip,
                unmatching_src_from_original: i.unmatching_src_from_original,
                payload_too_short_port: i.payload_too_short_port,
                unexpected_transport: i.unexpected_transport,
                unrecognized_vs: i.unrecognized_vs,
                forwarded_packets: i.forwarded_packets,
                broadcasted_packets: i.broadcasted_packets,
                packet_clones_sent: i.packet_clones_sent,
                packet_clones_received: i.packet_clones_received,
                packet_clone_failures: i.packet_clone_failures,
            }),
            common: s.common.as_ref().map(|c| CommonStatsJson {
                incoming_packets: c.incoming_packets,
                incoming_bytes: c.incoming_bytes,
                unexpected_network_proto: c.unexpected_network_proto,
                decap_successful: c.decap_successful,
                decap_failed: c.decap_failed,
                outgoing_packets: c.outgoing_packets,
                outgoing_bytes: c.outgoing_bytes,
            }),
            vs: s
                .vs
                .iter()
                .map(|v| NamedVsStatsJson {
                    vs: convert_vs_identifier(v.vs.as_ref()),
                    stats: v.stats.as_ref().map(|st| VsStatsJson {
                        incoming_packets: st.incoming_packets,
                        incoming_bytes: st.incoming_bytes,
                        packet_src_not_allowed: st.packet_src_not_allowed,
                        no_reals: st.no_reals,
                        ops_packets: st.ops_packets,
                        session_table_overflow: st.session_table_overflow,
                        echo_icmp_packets: st.echo_icmp_packets,
                        error_icmp_packets: st.error_icmp_packets,
                        real_is_disabled: st.real_is_disabled,
                        real_is_removed: st.real_is_removed,
                        not_rescheduled_packets: st.not_rescheduled_packets,
                        broadcasted_icmp_packets: st.broadcasted_icmp_packets,
                        created_sessions: st.created_sessions,
                        outgoing_packets: st.outgoing_packets,
                        outgoing_bytes: st.outgoing_bytes,
                    }),
                    reals: v
                        .reals
                        .iter()
                        .map(|r| NamedRealStatsJson {
                            real: convert_real_identifier(r.real.as_ref()),
                            stats: r.stats.as_ref().map(|st| RealStatsJson {
                                packets_real_disabled: st.packets_real_disabled,
                                packets_real_not_present: 0, // Field removed in new proto
                                ops_packets: st.ops_packets,
                                error_icmp_packets: st.error_icmp_packets,
                                created_sessions: st.created_sessions,
                                packets: st.packets,
                                bytes: st.bytes,
                            }),
                        })
                        .collect(),
                })
                .collect(),
        }),
    }
}

pub fn convert_show_sessions(response: &balancerpb::ShowSessionsResponse) -> ShowSessionsResponseJson {
    ShowSessionsResponseJson {
        sessions: response
            .sessions
            .iter()
            .map(|s| SessionInfoJson {
                client_addr: opt_addr_to_ip(&s.client_addr)
                    .map(|ip| ip.to_string())
                    .unwrap_or_default(),
                client_port: s.client_port,
                vs_id: convert_vs_identifier(s.vs_id.as_ref()),
                real_id: convert_real_identifier(s.real_id.as_ref()),
                create_timestamp: format_timestamp(s.create_timestamp.as_ref()),
                last_packet_timestamp: format_timestamp(s.last_packet_timestamp.as_ref()),
                timeout: format_duration(s.timeout.as_ref()),
            })
            .collect(),
    }
}

pub fn convert_show_graph(response: &balancerpb::ShowGraphResponse) -> ShowGraphResponseJson {
    ShowGraphResponseJson {
        graph: response.graph.as_ref().map(|g| GraphJson {
            virtual_services: g
                .virtual_services
                .iter()
                .map(|vs| GraphVsJson {
                    identifier: convert_vs_identifier(vs.identifier.as_ref()),
                    reals: vs
                        .reals
                        .iter()
                        .map(|r| GraphRealJson {
                            identifier: convert_relative_real_identifier(r.identifier.as_ref()),
                            weight: r.weight,
                            effective_weight: r.effective_weight,
                            enabled: r.enabled,
                        })
                        .collect(),
                })
                .collect(),
        }),
    }
}
