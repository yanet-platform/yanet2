//! JSON output with proper IP address formatting

use serde::Serialize;
use crate::rpc::balancerpb;
use crate::entities::bytes_to_ip;

////////////////////////////////////////////////////////////////////////////////
// ShowConfig JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct ShowConfigJson {
    pub target: Option<TargetJson>,
    pub module_config: Option<ModuleConfigJson>,
    pub module_state_config: Option<ModuleStateConfigJson>,
    pub buffered_real_updates: Vec<RealUpdateJson>,
}

#[derive(Serialize)]
pub struct TargetJson {
    pub config_name: String,
}

#[derive(Serialize)]
pub struct ModuleConfigJson {
    pub virtual_services: Vec<VirtualServiceJson>,
    pub source_address_v4: String,
    pub source_address_v6: String,
    pub decap_addresses: Vec<String>,
    pub sessions_timeouts: Option<SessionsTimeoutsJson>,
    pub wlc: Option<WlcConfigJson>,
}

#[derive(Serialize)]
pub struct VirtualServiceJson {
    pub addr: String,
    pub port: u32,
    pub proto: String,
    pub scheduler: String,
    pub allowed_srcs: Vec<SubnetJson>,
    pub reals: Vec<RealJson>,
    pub flags: Option<VsFlagsJson>,
    pub peers: Vec<String>,
}

#[derive(Serialize)]
pub struct SubnetJson {
    pub addr: String,
    pub size: u32,
}

#[derive(Serialize)]
pub struct RealJson {
    pub weight: u32,
    pub dst_addr: String,
    pub src_addr: String,
    pub src_mask: String,
    pub enabled: bool,
    pub port: u32,
}

#[derive(Serialize)]
pub struct VsFlagsJson {
    pub gre: bool,
    pub fix_mss: bool,
    pub ops: bool,
    pub pure_l3: bool,
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
pub struct WlcConfigJson {
    pub wlc_power: u64,
    pub max_real_weight: u64,
    pub update_period_ms: Option<i64>,
}

#[derive(Serialize)]
pub struct ModuleStateConfigJson {
    pub session_table_capacity: u64,
    pub session_table_scan_period_ms: Option<i64>,
    pub session_table_max_load_factor: f32,
}

#[derive(Serialize)]
pub struct RealUpdateJson {
    pub virtual_ip: String,
    pub port: u32,
    pub proto: String,
    pub real_ip: String,
    pub enable: bool,
    pub weight: Option<u32>,
}

////////////////////////////////////////////////////////////////////////////////
// StateInfo JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct StateInfoJson {
    pub target: Option<TargetJson>,
    pub info: Option<BalancerInfoJson>,
}

#[derive(Serialize)]
pub struct BalancerInfoJson {
    pub active_sessions: Option<AsyncInfoJson>,
    pub module: Option<ModuleStatsJson>,
    pub vs_info: Vec<VsInfoJson>,
    pub real_info: Vec<RealInfoJson>,
}

#[derive(Serialize)]
pub struct AsyncInfoJson {
    pub value: u64,
    pub updated_at: Option<String>,
}

#[derive(Serialize)]
pub struct VsInfoJson {
    pub vs_registry_idx: u32,
    pub vs_ip: String,
    pub vs_port: u32,
    pub vs_proto: String,
    pub active_sessions: Option<AsyncInfoJson>,
    pub last_packet_timestamp: Option<String>,
    pub stats: Option<VsInfoStatsJson>,
}

#[derive(Serialize)]
pub struct VsInfoStatsJson {
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
pub struct RealInfoJson {
    pub real_registry_idx: u32,
    pub vs_ip: String,
    pub vs_port: u32,
    pub vs_proto: String,
    pub real_ip: String,
    pub active_sessions: Option<AsyncInfoJson>,
    pub last_packet_timestamp: Option<String>,
    pub stats: Option<RealInfoStatsJson>,
}

#[derive(Serialize)]
pub struct RealInfoStatsJson {
    pub packets_real_disabled: u64,
    pub packets_real_not_present: u64,
    pub ops_packets: u64,
    pub error_icmp_packets: u64,
    pub created_sessions: u64,
    pub packets: u64,
    pub bytes: u64,
}

////////////////////////////////////////////////////////////////////////////////
// ConfigStats JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct ConfigStatsJson {
    pub target: Option<TargetJson>,
    pub device: String,
    pub pipeline: String,
    pub function: String,
    pub chain: String,
    pub stats: Option<BalancerStatsJson>,
}

#[derive(Serialize)]
pub struct BalancerStatsJson {
    pub module: Option<ModuleStatsJson>,
    pub vs: Vec<VsStatsInfoJson>,
    pub reals: Vec<RealStatsInfoJson>,
}

#[derive(Serialize)]
pub struct ModuleStatsJson {
    pub l4: Option<L4StatsJson>,
    pub icmpv4: Option<IcmpStatsJson>,
    pub icmpv6: Option<IcmpStatsJson>,
    pub common: Option<CommonStatsJson>,
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
pub struct VsStatsInfoJson {
    pub vs_registry_idx: u32,
    pub ip: String,
    pub port: u32,
    pub proto: String,
    pub stats: Option<VsStatsJson>,
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
pub struct RealStatsInfoJson {
    pub real_registry_idx: u32,
    pub vs_ip: String,
    pub port: u32,
    pub proto: String,
    pub real_ip: String,
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
// SessionsInfo JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct SessionsInfoJson {
    pub target: Option<TargetJson>,
    pub sessions_info: Vec<SessionInfoJson>,
}

#[derive(Serialize)]
pub struct SessionInfoJson {
    pub client_addr: String,
    pub client_port: u32,
    pub vs_addr: String,
    pub vs_port: u32,
    pub real_addr: String,
    pub real_port: u32,
    pub create_timestamp: Option<String>,
    pub last_packet_timestamp: Option<String>,
    pub timeout_seconds: Option<i64>,
}

////////////////////////////////////////////////////////////////////////////////
// ListConfigs JSON structures
////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
pub struct ListConfigsJson {
    pub configs: Vec<ShowConfigJson>,
}

////////////////////////////////////////////////////////////////////////////////
// Conversion functions
////////////////////////////////////////////////////////////////////////////////

fn proto_to_string(proto: i32) -> String {
    match balancerpb::TransportProto::try_from(proto) {
        Ok(balancerpb::TransportProto::Tcp) => "TCP".to_string(),
        Ok(balancerpb::TransportProto::Udp) => "UDP".to_string(),
        _ => format!("Unknown({})", proto),
    }
}

fn scheduler_to_string(sched: i32) -> String {
    match balancerpb::VsScheduler::try_from(sched) {
        Ok(balancerpb::VsScheduler::Wrr) => "WRR".to_string(),
        Ok(balancerpb::VsScheduler::Prr) => "PRR".to_string(),
        Ok(balancerpb::VsScheduler::Wlc) => "WLC".to_string(),
        _ => format!("Unknown({})", sched),
    }
}

fn format_timestamp(ts: Option<&prost_types::Timestamp>) -> Option<String> {
    ts.and_then(|t| {
        // Return None for zero timestamps (will be serialized as null in JSON)
        if t.seconds == 0 && t.nanos == 0 {
            return None;
        }
        
        use chrono::{DateTime, Utc};
        DateTime::<Utc>::from_timestamp(t.seconds, t.nanos as u32)
            .map(|dt| dt.to_rfc3339())
    })
}

pub fn convert_show_config(response: &balancerpb::ShowConfigResponse) -> ShowConfigJson {
    ShowConfigJson {
        target: response.target.as_ref().map(|t| TargetJson {
            config_name: t.config_name.clone(),
        }),
        module_config: response.module_config.as_ref().map(|c| ModuleConfigJson {
            virtual_services: c.virtual_services.iter().map(|vs| VirtualServiceJson {
                addr: bytes_to_ip(&vs.addr).map(|ip| ip.to_string()).unwrap_or_default(),
                port: vs.port,
                proto: proto_to_string(vs.proto),
                scheduler: scheduler_to_string(vs.scheduler),
                allowed_srcs: vs.allowed_srcs.iter().map(|s| SubnetJson {
                    addr: bytes_to_ip(&s.addr).map(|ip| ip.to_string()).unwrap_or_default(),
                    size: s.size,
                }).collect(),
                reals: vs.reals.iter().map(|r| RealJson {
                    weight: r.weight,
                    dst_addr: bytes_to_ip(&r.dst_addr).map(|ip| ip.to_string()).unwrap_or_default(),
                    src_addr: bytes_to_ip(&r.src_addr).map(|ip| ip.to_string()).unwrap_or_default(),
                    src_mask: bytes_to_ip(&r.src_mask).map(|ip| ip.to_string()).unwrap_or_default(),
                    enabled: r.enabled,
                    port: r.port,
                }).collect(),
                flags: vs.flags.as_ref().map(|f| VsFlagsJson {
                    gre: f.gre,
                    fix_mss: f.fix_mss,
                    ops: f.ops,
                    pure_l3: f.pure_l3,
                }),
                peers: vs.peers.iter().filter_map(|p| bytes_to_ip(p).ok().map(|ip| ip.to_string())).collect(),
            }).collect(),
            source_address_v4: bytes_to_ip(&c.source_address_v4).map(|ip| ip.to_string()).unwrap_or_default(),
            source_address_v6: bytes_to_ip(&c.source_address_v6).map(|ip| ip.to_string()).unwrap_or_default(),
            decap_addresses: c.decap_addresses.iter().filter_map(|a| bytes_to_ip(a).ok().map(|ip| ip.to_string())).collect(),
            sessions_timeouts: c.sessions_timeouts.as_ref().map(|t| SessionsTimeoutsJson {
                tcp_syn_ack: t.tcp_syn_ack,
                tcp_syn: t.tcp_syn,
                tcp_fin: t.tcp_fin,
                tcp: t.tcp,
                udp: t.udp,
                default: t.default,
            }),
            wlc: c.wlc.as_ref().map(|w| WlcConfigJson {
                wlc_power: w.wlc_power,
                max_real_weight: w.max_real_weight as u64,
                update_period_ms: w.update_period.as_ref().map(|p| p.seconds * 1000 + p.nanos as i64 / 1_000_000),
            }),
        }),
        module_state_config: response.module_state_config.as_ref().map(|s| ModuleStateConfigJson {
            session_table_capacity: s.session_table_capacity,
            session_table_scan_period_ms: s.session_table_scan_period.as_ref().map(|p| p.seconds * 1000 + p.nanos as i64 / 1_000_000),
            session_table_max_load_factor: s.session_table_max_load_factor,
        }),
        buffered_real_updates: response.buffered_real_updates.iter().map(|u| RealUpdateJson {
            virtual_ip: bytes_to_ip(&u.virtual_ip).map(|ip| ip.to_string()).unwrap_or_default(),
            port: u.port,
            proto: proto_to_string(u.proto),
            real_ip: bytes_to_ip(&u.real_ip).map(|ip| ip.to_string()).unwrap_or_default(),
            enable: u.enable,
            weight: if u.weight > 0 { Some(u.weight) } else { None },
        }).collect(),
    }
}

pub fn convert_list_configs(response: &balancerpb::ListConfigsResponse) -> ListConfigsJson {
    ListConfigsJson {
        configs: response.configs.iter().map(convert_show_config).collect(),
    }
}

pub fn convert_state_info(response: &balancerpb::StateInfoResponse) -> StateInfoJson {
    StateInfoJson {
        target: response.target.as_ref().map(|t| TargetJson {
            config_name: t.config_name.clone(),
        }),
        info: response.info.as_ref().map(|i| BalancerInfoJson {
            active_sessions: i.active_sessions.as_ref().map(|a| AsyncInfoJson {
                value: a.value,
                updated_at: format_timestamp(a.updated_at.as_ref()),
            }),
            module: i.module.as_ref().map(|m| ModuleStatsJson {
                l4: m.l4.as_ref().map(|l| L4StatsJson {
                    incoming_packets: l.incoming_packets,
                    select_vs_failed: l.select_vs_failed,
                    invalid_packets: l.invalid_packets,
                    select_real_failed: l.select_real_failed,
                    outgoing_packets: l.outgoing_packets,
                }),
                icmpv4: m.icmpv4.as_ref().map(|i| IcmpStatsJson {
                    incoming_packets: i.incoming_packets,
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
                icmpv6: m.icmpv6.as_ref().map(|i| IcmpStatsJson {
                    incoming_packets: i.incoming_packets,
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
                common: m.common.as_ref().map(|c| CommonStatsJson {
                    incoming_packets: c.incoming_packets,
                    incoming_bytes: c.incoming_bytes,
                    unexpected_network_proto: c.unexpected_network_proto,
                    decap_successful: c.decap_successful,
                    decap_failed: c.decap_failed,
                    outgoing_packets: c.outgoing_packets,
                    outgoing_bytes: c.outgoing_bytes,
                }),
            }),
            vs_info: i.vs_info.iter().map(|vs| VsInfoJson {
                vs_registry_idx: vs.vs_registry_idx,
                vs_ip: bytes_to_ip(&vs.vs_ip).map(|ip| ip.to_string()).unwrap_or_default(),
                vs_port: vs.vs_port,
                vs_proto: proto_to_string(vs.vs_proto),
                active_sessions: vs.active_sessions.as_ref().map(|a| AsyncInfoJson {
                    value: a.value,
                    updated_at: format_timestamp(a.updated_at.as_ref()),
                }),
                last_packet_timestamp: format_timestamp(vs.last_packet_timestamp.as_ref()),
                stats: vs.stats.as_ref().map(|s| VsInfoStatsJson {
                    incoming_packets: s.incoming_packets,
                    incoming_bytes: s.incoming_bytes,
                    packet_src_not_allowed: s.packet_src_not_allowed,
                    no_reals: s.no_reals,
                    ops_packets: s.ops_packets,
                    session_table_overflow: s.session_table_overflow,
                    echo_icmp_packets: s.echo_icmp_packets,
                    error_icmp_packets: s.error_icmp_packets,
                    real_is_disabled: s.real_is_disabled,
                    real_is_removed: s.real_is_removed,
                    not_rescheduled_packets: s.not_rescheduled_packets,
                    broadcasted_icmp_packets: s.broadcasted_icmp_packets,
                    created_sessions: s.created_sessions,
                    outgoing_packets: s.outgoing_packets,
                    outgoing_bytes: s.outgoing_bytes,
                }),
            }).collect(),
            real_info: i.real_info.iter().map(|r| RealInfoJson {
                real_registry_idx: r.real_registry_idx,
                vs_ip: bytes_to_ip(&r.vs_ip).map(|ip| ip.to_string()).unwrap_or_default(),
                vs_port: r.vs_port,
                vs_proto: proto_to_string(r.vs_proto),
                real_ip: bytes_to_ip(&r.real_ip).map(|ip| ip.to_string()).unwrap_or_default(),
                active_sessions: r.active_sessions.as_ref().map(|a| AsyncInfoJson {
                    value: a.value,
                    updated_at: format_timestamp(a.updated_at.as_ref()),
                }),
                last_packet_timestamp: format_timestamp(r.last_packet_timestamp.as_ref()),
                stats: r.stats.as_ref().map(|s| RealInfoStatsJson {
                    packets_real_disabled: s.packets_real_disabled,
                    packets_real_not_present: s.packets_real_not_present,
                    ops_packets: s.ops_packets,
                    error_icmp_packets: s.error_icmp_packets,
                    created_sessions: s.created_sessions,
                    packets: s.packets,
                    bytes: s.bytes,
                }),
            }).collect(),
        }),
    }
}

pub fn convert_config_stats(response: &balancerpb::ConfigStatsResponse) -> ConfigStatsJson {
    ConfigStatsJson {
        target: response.target.as_ref().map(|t| TargetJson {
            config_name: t.config_name.clone(),
        }),
        device: response.device.clone(),
        pipeline: response.pipeline.clone(),
        function: response.function.clone(),
        chain: response.chain.clone(),
        stats: response.stats.as_ref().map(|s| BalancerStatsJson {
            module: s.module.as_ref().map(|m| ModuleStatsJson {
                l4: m.l4.as_ref().map(|l| L4StatsJson {
                    incoming_packets: l.incoming_packets,
                    select_vs_failed: l.select_vs_failed,
                    invalid_packets: l.invalid_packets,
                    select_real_failed: l.select_real_failed,
                    outgoing_packets: l.outgoing_packets,
                }),
                icmpv4: m.icmpv4.as_ref().map(|i| IcmpStatsJson {
                    incoming_packets: i.incoming_packets,
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
                icmpv6: m.icmpv6.as_ref().map(|i| IcmpStatsJson {
                    incoming_packets: i.incoming_packets,
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
                common: m.common.as_ref().map(|c| CommonStatsJson {
                    incoming_packets: c.incoming_packets,
                    incoming_bytes: c.incoming_bytes,
                    unexpected_network_proto: c.unexpected_network_proto,
                    decap_successful: c.decap_successful,
                    decap_failed: c.decap_failed,
                    outgoing_packets: c.outgoing_packets,
                    outgoing_bytes: c.outgoing_bytes,
                }),
            }),
            vs: s.vs.iter().map(|vs| VsStatsInfoJson {
                vs_registry_idx: vs.vs_registry_idx,
                ip: bytes_to_ip(&vs.ip).map(|ip| ip.to_string()).unwrap_or_default(),
                port: vs.port,
                proto: proto_to_string(vs.proto),
                stats: vs.stats.as_ref().map(|st| VsStatsJson {
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
            }).collect(),
            reals: s.reals.iter().map(|r| RealStatsInfoJson {
                real_registry_idx: r.real_registry_idx,
                vs_ip: bytes_to_ip(&r.vs_ip).map(|ip| ip.to_string()).unwrap_or_default(),
                port: r.port,
                proto: proto_to_string(r.proto),
                real_ip: bytes_to_ip(&r.real_ip).map(|ip| ip.to_string()).unwrap_or_default(),
                stats: r.stats.as_ref().map(|st| RealStatsJson {
                    packets_real_disabled: st.packets_real_disabled,
                    packets_real_not_present: st.packets_real_not_present,
                    ops_packets: st.ops_packets,
                    error_icmp_packets: st.error_icmp_packets,
                    created_sessions: st.created_sessions,
                    packets: st.packets,
                    bytes: st.bytes,
                }),
            }).collect(),
        }),
    }
}

pub fn convert_sessions_info(response: &balancerpb::SessionsInfoResponse) -> SessionsInfoJson {
    SessionsInfoJson {
        target: response.target.as_ref().map(|t| TargetJson {
            config_name: t.config_name.clone(),
        }),
        sessions_info: response.sessions_info.iter().map(|s| SessionInfoJson {
            client_addr: bytes_to_ip(&s.client_addr).map(|ip| ip.to_string()).unwrap_or_default(),
            client_port: s.client_port,
            vs_addr: bytes_to_ip(&s.vs_addr).map(|ip| ip.to_string()).unwrap_or_default(),
            vs_port: s.vs_port,
            real_addr: bytes_to_ip(&s.real_addr).map(|ip| ip.to_string()).unwrap_or_default(),
            real_port: s.real_port,
            create_timestamp: format_timestamp(s.create_timestamp.as_ref()),
            last_packet_timestamp: format_timestamp(s.last_packet_timestamp.as_ref()),
            timeout_seconds: s.timeout.as_ref().map(|t| t.seconds),
        }).collect(),
    }
}