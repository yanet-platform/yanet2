use std::collections::HashMap;

use tabled::Tabled;
use yanet_cli_balancer2::balancerpb;
use ync::display::print_table_from_entries;

use crate::{bytes_to_ip, format_ip_port};

fn print_module_stats(state: &balancerpb::BalancerState) {
    println!("Module:");

    let mut rows: Vec<StatsRow> = Vec::new();

    if let Some(c) = &state.common_stats {
        rows.push(StatsRow::new("Common", "Incoming Pkts", c.incoming_packets));
        rows.push(StatsRow::new("", "Incoming Bytes", c.incoming_bytes));
        rows.push(StatsRow::new("", "Unexpected Net Proto", c.unexpected_network_proto));
        rows.push(StatsRow::new(
            "",
            "Unexpected Trans Proto",
            c.unexpected_transport_proto,
        ));
        rows.push(StatsRow::new("", "Decap Success", c.decap_successful));
        rows.push(StatsRow::new("", "Decap Failed", c.decap_failed));
        rows.push(StatsRow::new("", "Outgoing Pkts", c.outgoing_packets));
        rows.push(StatsRow::new("", "Outgoing Bytes", c.outgoing_bytes));
        rows.push(StatsRow::new("", "No Headroom Pkts", c.no_headroom_packets));
    }

    if let Some(l) = &state.l4_stats {
        rows.push(StatsRow::new("L4", "Incoming Pkts", l.incoming_packets));
        rows.push(StatsRow::new("", "Outgoing Pkts", l.outgoing_packets));
        rows.push(StatsRow::new("", "Select VS Fail", l.select_vs_failed));
        rows.push(StatsRow::new("", "Select Real Fail", l.select_real_failed));
        rows.push(StatsRow::new("", "Invalid Pkts", l.invalid_packets));
    }

    if let Some(icmp) = &state.icmp_ip4_stats {
        push_icmp_rows(&mut rows, "ICMPv4", icmp);
    }

    if let Some(icmp) = &state.icmp_ip6_stats {
        push_icmp_rows(&mut rows, "ICMPv6", icmp);
    }

    print_table_from_entries(rows);
    println!();
}

type IcmpField = (&'static str, fn(&balancerpb::IcmpStats) -> u64);

const ICMP_FIELDS: &[IcmpField] = &[
    ("Incoming Pkts", |s| s.incoming_packets),
    ("Src Not Allowed", |s| s.src_not_allowed),
    ("Echo Responses", |s| s.echo_responses),
    ("Payload Short IP", |s| s.payload_too_short_ip),
    ("Unmatch Src Orig", |s| s.unmatching_src_from_original),
    ("Payload Short Port", |s| s.payload_too_short_port),
    ("Unexpected Trans", |s| s.unexpected_transport),
    ("Unrecognized VS", |s| s.unrecognized_vs),
    ("Forwarded Pkts", |s| s.forwarded_packets),
    ("Broadcasted Pkts", |s| s.broadcasted_packets),
    ("Clones Sent", |s| s.packet_clones_sent),
    ("Clones Received", |s| s.packet_clones_received),
    ("Clone Failures", |s| s.packet_clone_failures),
];

fn push_icmp_rows(rows: &mut Vec<StatsRow>, category: &str, icmp: &balancerpb::IcmpStats) {
    for (idx, (name, get)) in ICMP_FIELDS.iter().enumerate() {
        let cat = if idx == 0 { category } else { "" };
        rows.push(StatsRow::new(cat, name, get(icmp)));
    }
}

pub struct ShowOptions {
    pub stats: bool,
    pub acl: bool,
    pub peers: bool,
    pub decap: bool,
}

pub fn print_table_view(states: &[balancerpb::BalancerState], opts: &ShowOptions) {
    for (idx, state) in states.iter().enumerate() {
        if idx > 0 {
            println!();
        }
        print_table_view_state(state, opts);
    }
}

fn print_table_view_state(state: &balancerpb::BalancerState, opts: &ShowOptions) {
    println!("Balancer: {}", state.config_name);
    if !state.sessions_state_name.is_empty() {
        println!(
            "Sessions State: {} (capacity: {})",
            state.sessions_state_name, state.sessions_state_capacity,
        );
    }
    if let Some(r) = &state.r#ref {
        print_ref_inline(r);
    }

    if opts.stats {
        println!("Active Sessions: {}", state.active_sessions);
        println!(
            "Last Packet: {}",
            state
                .last_packet_timestamp
                .as_ref()
                .map_or_else(|| "N/A".to_string(), format_timestamp),
        );
        println!();
    }

    if opts.decap {
        print_decap(state);
    }

    if opts.stats {
        print_module_stats(state);
    }

    for vs in &state.vs {
        print_table_view_vs(vs, opts);
    }
}

fn print_table_view_vs(vs: &balancerpb::VsState, opts: &ShowOptions) {
    let Some(cfg) = &vs.config else { return };
    let Some(id) = &cfg.id else { return };
    let Some(addr_port) = fmt_addr_port(&id.addr, id.port) else {
        return;
    };
    let proto = proto_str(id.proto).unwrap_or("???");
    let scheduler = scheduler_str(cfg.scheduler).unwrap_or("???");
    let flags = flags_str(cfg.flags.as_ref());

    println!("VS {}/{}:", addr_port, proto);
    println!("  Scheduler: {}", scheduler);
    if !flags.is_empty() {
        println!("  Flags: {}", flags);
    }

    if opts.stats {
        println!("  Active Sessions: {}", vs.active_sessions);
        if let Some(ts) = &vs.last_packet_timestamp {
            println!("  Last Packet: {}", format_timestamp(ts));
        }
        if let Some(stats) = &vs.stats {
            print_vs_stats(stats);
        }
    }

    if opts.peers {
        print_vs_peers(cfg);
    }

    if opts.acl {
        print_vs_acl(cfg, &vs.allowed_sources_stats, opts.stats);
    }

    if opts.stats {
        let rows: Vec<RealStatsRow> = vs.reals.iter().filter_map(real_stats_row).collect();
        if !rows.is_empty() {
            print_table_from_entries(rows);
        }
    } else {
        let rows: Vec<RealBasicRow> = vs.reals.iter().filter_map(real_basic_row).collect();
        if !rows.is_empty() {
            print_table_from_entries(rows);
        }
    }
    println!();
}

fn real_display_addr(real: &balancerpb::RealState) -> Option<(String, &balancerpb::RealConfig)> {
    let Some(rcfg) = real.config.as_ref() else {
        log::warn!("dropped real row: missing config");
        return None;
    };
    let Some(rid) = rcfg.id.as_ref() else {
        log::warn!("dropped real row: missing real id");
        return None;
    };
    let rip = match bytes_to_ip(&rid.ip) {
        Ok(ip) => ip,
        Err(e) => {
            log::warn!("dropped real row: invalid ip bytes: {e}");
            return None;
        }
    };
    let rport = match u16::try_from(rid.port) {
        Ok(p) => p,
        Err(_) => {
            log::warn!("dropped real row: port out of u16 range: {}", rid.port);
            return None;
        }
    };
    Some((format_ip_port(rip, rport), rcfg))
}

fn real_basic_row(real: &balancerpb::RealState) -> Option<RealBasicRow> {
    let (real_str, rcfg) = real_display_addr(real)?;
    Some(RealBasicRow {
        real: real_str,
        enabled: real.enabled,
        weight: rcfg.weight,
        effective_weight: real.effective_weight,
    })
}

fn real_stats_row(real: &balancerpb::RealState) -> Option<RealStatsRow> {
    let (real_str, rcfg) = real_display_addr(real)?;
    let rs = real.stats.as_ref();
    Some(RealStatsRow {
        real: real_str,
        enabled: real.enabled,
        weight: rcfg.weight,
        effective_weight: real.effective_weight,
        packets: rs.map_or(0, |s| s.packets),
        bytes: rs.map_or(0, |s| s.bytes),
        active_sessions: real.active_sessions,
        created_sessions: rs.map_or(0, |s| s.created_sessions),
        last_packet: real
            .last_packet_timestamp
            .as_ref()
            .map_or_else(|| "-".to_string(), format_timestamp),
        disabled_pkts: rs.map_or(0, |s| s.packets_real_disabled),
        icmp_pkts: rs.map_or(0, |s| s.error_icmp_packets),
    })
}

fn print_vs_stats(stats: &balancerpb::VsStats) {
    println!("  Incoming Packets: {}", stats.incoming_packets);
    println!("  Incoming Bytes: {}", stats.incoming_bytes);
    println!("  Outgoing Packets: {}", stats.outgoing_packets);
    println!("  Outgoing Bytes: {}", stats.outgoing_bytes);
    println!("  Created Sessions: {}", stats.created_sessions);
    println!("  Packet Src Not Allowed: {}", stats.packet_src_not_allowed);
    println!("  No Reals: {}", stats.no_reals);
    println!("  Session Table Overflow: {}", stats.session_table_overflow);
    println!("  Echo ICMP Packets: {}", stats.echo_icmp_packets);
    println!("  Error ICMP Packets: {}", stats.error_icmp_packets);
    println!("  Real Is Disabled: {}", stats.real_is_disabled);
    println!("  Not Rescheduled Packets: {}", stats.not_rescheduled_packets);
    println!("  Broadcasted ICMP Packets: {}", stats.broadcasted_icmp_packets);
    println!("  Fix MSS Malformed: {}", stats.fix_mss_malformed);
}

fn print_vs_acl(cfg: &balancerpb::VsConfig, stats: &[balancerpb::AllowedSourcesStats], with_stats: bool) {
    if cfg.allowed_sources.is_empty() {
        return;
    }

    let stats_map: HashMap<&str, u64> = stats.iter().map(|s| (s.tag.as_str(), s.passes)).collect();

    println!("  Allowed Sources:");
    for src in &cfg.allowed_sources {
        print_allowed_source(src, with_stats.then_some(&stats_map));
    }
}

fn print_allowed_source(src: &balancerpb::AllowedSources, stats_map: Option<&HashMap<&str, u64>>) {
    let tag = src.tag.as_deref().unwrap_or("");
    if !tag.is_empty() {
        match stats_map {
            Some(map) => {
                let passes = map.get(tag).copied().unwrap_or(0);
                println!("    Tag: {} (passes: {})", tag, passes);
            }
            None => println!("    Tag: {}", tag),
        }
    }
    for net in &src.nets {
        println!("    Net: {}", net);
    }
    for pr in &src.ports {
        if pr.from == pr.to {
            println!("    Port: {}", pr.from);
        } else {
            println!("    Ports: {}-{}", pr.from, pr.to);
        }
    }
}

fn print_vs_peers(cfg: &balancerpb::VsConfig) {
    if cfg.peers.is_empty() {
        return;
    }
    println!("  Peers:");
    for peer in &cfg.peers {
        if let Ok(ip) = bytes_to_ip(peer) {
            println!("    {}", ip);
        }
    }
}

fn print_decap(state: &balancerpb::BalancerState) {
    let Some(addr) = &state.addr else {
        return;
    };
    if !addr.source_ip4.is_empty() {
        if let Ok(ip) = bytes_to_ip(&addr.source_ip4) {
            println!("Source IPv4: {}", ip);
        }
    }
    if !addr.source_ip6.is_empty() {
        if let Ok(ip) = bytes_to_ip(&addr.source_ip6) {
            println!("Source IPv6: {}", ip);
        }
    }
    if !addr.decaps.is_empty() {
        println!("Decap Addresses:");
        for a in &addr.decaps {
            if let Ok(ip) = bytes_to_ip(a) {
                println!("  {}", ip);
            }
        }
    }
    println!();
}

#[derive(Tabled)]
struct RealBasicRow {
    #[tabled(rename = "Real")]
    real: String,
    #[tabled(rename = "Enabled")]
    enabled: bool,
    #[tabled(rename = "Wght")]
    weight: u32,
    #[tabled(rename = "Eff Wght")]
    effective_weight: u64,
}

// Streaming session output uses fixed-width columns rather than `Tabled`,
// which would buffer the full result set.
pub fn print_sessions_header() {
    println!(
        "{:<40} {:<40} {:<50} {:<8} {:<8} {:<8}",
        "VS", "Real", "Client", "Expires", "Timeout", "Age",
    );
}

/// Format a wire-format addr+port pair. Returns None on bad address bytes
/// or u16 overflow; port 0 is omitted from the output.
fn fmt_addr_port(addr: &[u8], port: u32) -> Option<String> {
    let ip = bytes_to_ip(addr).ok()?;
    let port = u16::try_from(port).ok()?;
    Some(format_ip_port(ip, port))
}

pub fn print_session(session: &balancerpb::Session, now: i64) {
    let vs = session
        .vs_id
        .as_ref()
        .and_then(|id| {
            Some(format!(
                "{}/{}",
                fmt_addr_port(&id.addr, id.port)?,
                proto_str(id.proto).unwrap_or("???")
            ))
        })
        .unwrap_or_else(|| "-".to_string());
    let real = session
        .real_id
        .as_ref()
        .and_then(|r| fmt_addr_port(&r.ip, r.port))
        .unwrap_or_else(|| "-".to_string());
    let client = fmt_addr_port(&session.client_addr, session.client_port).unwrap_or_else(|| "-".to_string());
    let expires = match (session.last_packet_timestamp.as_ref(), session.timeout.as_ref()) {
        (Some(last), Some(timeout)) => format!("{}", (last.seconds + timeout.seconds - now).max(0)),
        _ => "-".to_string(),
    };
    let timeout = session
        .timeout
        .as_ref()
        .map_or_else(|| "-".to_string(), |d| d.seconds.to_string());
    let age = session
        .create_timestamp
        .as_ref()
        .map_or_else(|| "-".to_string(), |ts| (now - ts.seconds).max(0).to_string());

    println!(
        "{:<40} {:<40} {:<50} {:<8} {:<8} {:<8}",
        vs, real, client, expires, timeout, age,
    );
}

#[derive(Tabled)]
struct StatsRow {
    #[tabled(rename = "Category")]
    category: String,
    #[tabled(rename = "Metric")]
    metric: String,
    #[tabled(rename = "Value")]
    value: u64,
}

impl StatsRow {
    fn new(category: &str, metric: &str, value: u64) -> Self {
        Self {
            category: category.to_string(),
            metric: metric.to_string(),
            value,
        }
    }
}

#[derive(Tabled)]
struct RealStatsRow {
    #[tabled(rename = "Real")]
    real: String,
    #[tabled(rename = "Enabled")]
    enabled: bool,
    #[tabled(rename = "Wght")]
    weight: u32,
    #[tabled(rename = "Eff Wght")]
    effective_weight: u64,
    #[tabled(rename = "Pkts")]
    packets: u64,
    #[tabled(rename = "Bytes")]
    bytes: u64,
    #[tabled(rename = "Last Pkt")]
    last_packet: String,
    #[tabled(rename = "Dis Pkts")]
    disabled_pkts: u64,
    #[tabled(rename = "ICMP Err")]
    icmp_pkts: u64,
    #[tabled(rename = "Sess Act")]
    active_sessions: u64,
    #[tabled(rename = "Sess Crt")]
    created_sessions: u64,
}

pub fn prettify_json(value: &mut serde_json::Value) {
    match value {
        serde_json::Value::Array(arr) => {
            if let Some(ip) = bytes_array_to_ip_string(arr) {
                *value = serde_json::Value::String(ip);
            } else {
                for item in arr.iter_mut() {
                    prettify_json(item);
                }
            }
        }
        serde_json::Value::Object(map) => {
            prettify_enum(map, "scheduler", scheduler_str);
            prettify_enum(map, "proto", proto_str);
            for (_, v) in map.iter_mut() {
                prettify_json(v);
            }
        }
        _ => {}
    }
}

fn prettify_enum(
    map: &mut serde_json::Map<String, serde_json::Value>,
    key: &str,
    to_str: fn(i32) -> Option<&'static str>,
) {
    if let Some(val) = map.get(key).and_then(|v| v.as_i64())
        && let Some(name) = to_str(val as i32)
    {
        map.insert(key.to_string(), serde_json::Value::String(name.to_string()));
    }
}

fn bytes_array_to_ip_string(arr: &[serde_json::Value]) -> Option<String> {
    if arr.len() != 4 && arr.len() != 16 {
        return None;
    }
    let bytes: Vec<u8> = arr
        .iter()
        .map(|v| v.as_u64().and_then(|n| u8::try_from(n).ok()))
        .collect::<Option<Vec<_>>>()?;
    Some(crate::bytes_to_ip(&bytes).ok()?.to_string())
}

fn proto_str(proto: i32) -> Option<&'static str> {
    match balancerpb::TransportProto::try_from(proto).ok()? {
        balancerpb::TransportProto::Tcp => Some("tcp"),
        balancerpb::TransportProto::Udp => Some("udp"),
    }
}

fn scheduler_str(scheduler: i32) -> Option<&'static str> {
    match balancerpb::VsScheduler::try_from(scheduler).ok()? {
        balancerpb::VsScheduler::Sh => Some("sh"),
        balancerpb::VsScheduler::Wrr => Some("wrr"),
        balancerpb::VsScheduler::Wlc => Some("wlc"),
        balancerpb::VsScheduler::Op => Some("op"),
    }
}

fn flags_str(flags: Option<&balancerpb::VsFlags>) -> String {
    let Some(f) = flags else {
        return String::new();
    };
    let mut parts = Vec::new();
    if f.gre {
        parts.push("gre");
    }
    if f.fix_mss {
        parts.push("mss");
    }
    if f.pure_l3 {
        parts.push("l3");
    }
    parts.join(",")
}

fn print_ref_inline(r: &balancerpb::PacketHandlerRef) {
    let mut parts = Vec::new();
    if let Some(d) = &r.device {
        parts.push(format!("Device: {}", d));
    }
    if let Some(p) = &r.pipeline {
        parts.push(format!("Pipeline: {}", p));
    }
    if let Some(f) = &r.function {
        parts.push(format!("Function: {}", f));
    }
    if let Some(c) = &r.chain {
        parts.push(format!("Chain: {}", c));
    }
    if !parts.is_empty() {
        println!("{}", parts.join(" | "));
    }
}

fn format_timestamp(ts: &prost_types::Timestamp) -> String {
    if ts.seconds == 0 && ts.nanos == 0 {
        return "N/A".to_string();
    }
    let ndt = chrono::DateTime::from_timestamp(ts.seconds, ts.nanos as u32);
    match ndt {
        Some(dt) => dt.format("%Y-%m-%d %H:%M:%S").to_string(),
        None => "-".to_string(),
    }
}
