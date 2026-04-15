use tabled::{
    Table, Tabled,
    settings::{
        Color, Style,
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
    },
};
use yanet_cli_balancer::balancerpb;

use crate::{bytes_to_ip, format_ip_port};

// ─── Compact (IPVS-style) Output ────────────────────────────────────────────

pub fn print_compact(state: &balancerpb::BalancerState) {
    println!("Balancer: {}", state.balancer_name);
    println!("Active Sessions: {}", format_number(state.active_sessions));
    println!();
    const LINE_WIDTH: usize = 112;

    println!("{:<46}{:<8}Flags", "VirtualService", "Sched",);
    println!(
        "  -> {:<38}{:<10}{:<10}{:<12}{:<18}{:<18}",
        "RemoteAddress:Port", "Enabled", "Weight", "Conns", "Pkts", "Bytes",
    );
    println!("{}", "\u{2500}".repeat(LINE_WIDTH));

    for (i, vs) in state.virtual_services.iter().enumerate() {
        let Some(id) = &vs.id else { continue };
        let ip = match bytes_to_ip(&id.addr) {
            Ok(ip) => ip,
            Err(_) => continue,
        };
        if i > 0 {
            println!("{}", "\u{2500}".repeat(LINE_WIDTH));
        }
        let proto = proto_str(id.proto);
        let scheduler = scheduler_str(vs.scheduler);
        let flags = flags_str(vs.flags.as_ref());
        let vs_str = format!("{}/{}", format_ip_port(ip, id.port), proto);

        println!("{:<46}{:<8}{}", vs_str, scheduler, flags);

        for real in &vs.reals {
            let Some(rid) = &real.id else { continue };
            let rip = match bytes_to_ip(&rid.ip) {
                Ok(ip) => ip,
                Err(_) => continue,
            };
            let real_addr = format_ip_port(rip, rid.port);
            let rs = real.real_stats.as_ref();
            let enabled = if real.enabled { "true" } else { "false" };
            println!(
                "  -> {:<38}{:<10}{:<10}{:<12}{:<18}{:<18}",
                real_addr,
                enabled,
                format_number(real.weight),
                format_number(real.active_sessions),
                format_number(rs.map_or(0, |s| s.packets)),
                format_number(rs.map_or(0, |s| s.bytes)),
            );
        }
    }
}

// ─── Module Stats ──────────────────────────────────────────────────────────

fn print_module_stats(state: &balancerpb::BalancerState) {
    println!("Module:");

    let mut rows: Vec<StatsRow> = Vec::new();

    if let Some(c) = &state.common_stats {
        rows.push(StatsRow::new(
            "Common",
            "Incoming Pkts",
            format_number(c.incoming_packets),
        ));
        rows.push(StatsRow::new("", "Incoming Bytes", format_number(c.incoming_bytes)));
        rows.push(StatsRow::new(
            "",
            "Unexpected Proto",
            format_number(c.unexpected_network_proto),
        ));
        rows.push(StatsRow::new("", "Decap Success", format_number(c.decap_successful)));
        rows.push(StatsRow::new("", "Decap Failed", format_number(c.decap_failed)));
        rows.push(StatsRow::new("", "Outgoing Pkts", format_number(c.outgoing_packets)));
        rows.push(StatsRow::new("", "Outgoing Bytes", format_number(c.outgoing_bytes)));
        rows.push(StatsRow::empty());
    }

    if let Some(l) = &state.l4_stats {
        rows.push(StatsRow::new("L4", "Incoming Pkts", format_number(l.incoming_packets)));
        rows.push(StatsRow::new("", "Outgoing Pkts", format_number(l.outgoing_packets)));
        rows.push(StatsRow::new("", "Select VS Fail", format_number(l.select_vs_failed)));
        rows.push(StatsRow::new(
            "",
            "Select Real Fail",
            format_number(l.select_real_failed),
        ));
        rows.push(StatsRow::new("", "Invalid Pkts", format_number(l.invalid_packets)));
        rows.push(StatsRow::empty());
    }

    if let Some(icmp) = &state.icmp_ipv4_stats {
        push_icmp_rows(&mut rows, "ICMPv4", icmp);
        rows.push(StatsRow::empty());
    }

    if let Some(icmp) = &state.icmp_ipv6_stats {
        push_icmp_rows(&mut rows, "ICMPv6", icmp);
    }

    // Remove trailing empty row.
    if rows
        .last()
        .is_some_and(|r| r.category.is_empty() && r.metric.is_empty())
    {
        rows.pop();
    }

    print_table(rows);
    println!();
}

fn push_icmp_rows(rows: &mut Vec<StatsRow>, category: &str, icmp: &balancerpb::IcmpStats) {
    rows.push(StatsRow::new(
        category,
        "Incoming Pkts",
        format_number(icmp.incoming_packets),
    ));
    rows.push(StatsRow::new(
        "",
        "Src Not Allowed",
        format_number(icmp.src_not_allowed),
    ));
    rows.push(StatsRow::new("", "Echo Responses", format_number(icmp.echo_responses)));
    rows.push(StatsRow::new(
        "",
        "Payload Short IP",
        format_number(icmp.payload_too_short_ip),
    ));
    rows.push(StatsRow::new(
        "",
        "Unmatch Src Orig",
        format_number(icmp.unmatching_src_from_original),
    ));
    rows.push(StatsRow::new(
        "",
        "Payload Short Port",
        format_number(icmp.payload_too_short_port),
    ));
    rows.push(StatsRow::new(
        "",
        "Unexpected Trans",
        format_number(icmp.unexpected_transport),
    ));
    rows.push(StatsRow::new(
        "",
        "Unrecognized VS",
        format_number(icmp.unrecognized_vs),
    ));
    rows.push(StatsRow::new(
        "",
        "Forwarded Pkts",
        format_number(icmp.forwarded_packets),
    ));
    rows.push(StatsRow::new(
        "",
        "Broadcasted Pkts",
        format_number(icmp.broadcasted_packets),
    ));
    rows.push(StatsRow::new("", "Clones Sent", format_number(icmp.packet_clones_sent)));
    rows.push(StatsRow::new(
        "",
        "Clones Received",
        format_number(icmp.packet_clones_received),
    ));
    rows.push(StatsRow::new(
        "",
        "Clone Failures",
        format_number(icmp.packet_clone_failures),
    ));
}

// ─── Table View Output ─────────────────────────────────────────────────────

pub struct ShowOptions {
    pub stats: bool,
    pub acl: bool,
    pub peers: bool,
    pub decap: bool,
}

pub fn print_table_view(states: &[balancerpb::BalancerState], opts: &ShowOptions) {
    for (i, state) in states.iter().enumerate() {
        if i > 0 {
            println!();
        }
        print_table_view_state(state, opts);
    }
}

fn print_table_view_state(state: &balancerpb::BalancerState, opts: &ShowOptions) {
    println!("Balancer: {}", state.balancer_name);
    if let Some(r) = &state.r#ref {
        print_ref_inline(r);
    }

    if opts.stats {
        println!("Active Sessions: {}", format_number(state.active_sessions));
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

    for vs in &state.virtual_services {
        print_table_view_vs(vs, opts);
    }
}

fn print_table_view_vs(vs: &balancerpb::VsState, opts: &ShowOptions) {
    let Some(id) = &vs.id else { return };
    let ip = match bytes_to_ip(&id.addr) {
        Ok(ip) => ip,
        Err(_) => return,
    };
    let proto = proto_str(id.proto).to_uppercase();
    let addr_port = format_ip_port(ip, id.port);
    let scheduler = scheduler_str(vs.scheduler);
    let flags = flags_str(vs.flags.as_ref());

    println!("VS {}/{}:", addr_port, proto);
    println!("  Scheduler: {}", scheduler);
    if !flags.is_empty() {
        println!("  Flags: {}", flags);
    }

    if opts.stats {
        println!("  Active Sessions: {}", format_number(vs.active_sessions));
        if let Some(ts) = &vs.last_packet_timestamp {
            println!("  Last Packet: {}", format_timestamp(ts));
        }
        if let Some(stats) = &vs.stats {
            print_vs_stats(stats);
        }
    }

    if opts.peers {
        print_vs_peers(vs);
    }

    if opts.acl {
        print_vs_acl(vs, opts.stats);
    }

    // Reals table.
    let real_rows: Vec<_> = vs
        .reals
        .iter()
        .filter_map(|real| {
            let rid = real.id.as_ref()?;
            let rip = bytes_to_ip(&rid.ip).ok()?;
            let real_addr = format_ip_port(rip, rid.port);

            if opts.stats {
                let rs = real.real_stats.as_ref();
                Some(RealTableRow::Stats(RealStatsRow {
                    real: real_addr,
                    enabled: if real.enabled {
                        "true".to_string()
                    } else {
                        "false".to_string()
                    },
                    weight: format_number(real.weight),
                    effective_weight: format_number(real.effective_weight),
                    packets: format_number(rs.map_or(0, |s| s.packets)),
                    bytes: format_number(rs.map_or(0, |s| s.bytes)),
                    active_sessions: format_number(real.active_sessions),
                    created_sessions: format_number(real.real_stats.map_or(0, |s| s.created_sessions)),
                    last_packet: real
                        .last_packet_timestamp
                        .as_ref()
                        .map_or_else(|| "-".to_string(), format_timestamp),
                    disabled_pkts: format_number(rs.map_or(0, |s| s.packets_real_disabled)),
                    icmp_pkts: format_number(rs.map_or(0, |s| s.error_icmp_packets)),
                }))
            } else {
                Some(RealTableRow::Basic(RealBasicRow {
                    real: real_addr,
                    weight: format_number(real.weight),
                    effective_weight: format_number(real.effective_weight),
                    enabled: if real.enabled {
                        "true".to_string()
                    } else {
                        "false".to_string()
                    },
                }))
            }
        })
        .collect();

    if !real_rows.is_empty() {
        match &real_rows[0] {
            RealTableRow::Stats(_) => {
                let rows: Vec<RealStatsRow> = real_rows
                    .into_iter()
                    .filter_map(|r| match r {
                        RealTableRow::Stats(s) => Some(s),
                        _ => None,
                    })
                    .collect();
                print_table(rows);
            }
            RealTableRow::Basic(_) => {
                let rows: Vec<RealBasicRow> = real_rows
                    .into_iter()
                    .filter_map(|r| match r {
                        RealTableRow::Basic(b) => Some(b),
                        _ => None,
                    })
                    .collect();
                print_table(rows);
            }
        }
    }
    println!();
}

fn print_vs_stats(stats: &balancerpb::VsStats) {
    println!("  Incoming Packets: {}", format_number(stats.incoming_packets));
    println!("  Incoming Bytes: {}", format_number(stats.incoming_bytes));
    println!("  Outgoing Packets: {}", format_number(stats.outgoing_packets));
    println!("  Outgoing Bytes: {}", format_number(stats.outgoing_bytes));
    println!("  Created Sessions: {}", format_number(stats.created_sessions));
    println!(
        "  Packet Src Not Allowed: {}",
        format_number(stats.packet_src_not_allowed)
    );
    println!("  No Reals: {}", format_number(stats.no_reals));
    println!(
        "  Session Table Overflow: {}",
        format_number(stats.session_table_overflow)
    );
    println!("  Echo ICMP Packets: {}", format_number(stats.echo_icmp_packets));
    println!("  Error ICMP Packets: {}", format_number(stats.error_icmp_packets));
    println!("  Real Is Disabled: {}", format_number(stats.real_is_disabled));
    println!("  Real Is Removed: {}", format_number(stats.real_is_removed));
    println!(
        "  Not Rescheduled Packets: {}",
        format_number(stats.not_rescheduled_packets)
    );
    println!(
        "  Broadcasted ICMP Packets: {}",
        format_number(stats.broadcasted_icmp_packets)
    );
}

fn print_vs_acl(vs: &balancerpb::VsState, with_stats: bool) {
    if vs.allowed_srcs_config.is_empty() {
        return;
    }

    // Build a map from tag -> passes for stats lookup.
    let stats_map: std::collections::HashMap<&str, u64> =
        vs.allowed_sources.iter().map(|s| (s.tag.as_str(), s.passes)).collect();

    println!("  Allowed Sources:");
    for src in &vs.allowed_srcs_config {
        let tag = src.tag.as_deref().unwrap_or("");
        if !tag.is_empty() {
            if with_stats {
                let passes = stats_map.get(tag).copied().unwrap_or(0);
                println!("    Tag: {} (passes: {})", tag, format_number(passes));
            } else {
                println!("    Tag: {}", tag);
            }
        }
        for net in &src.nets {
            let addr = bytes_to_ip(&net.addr)
                .map(|ip| ip.to_string())
                .unwrap_or_else(|_| "?".to_string());
            let mask = bytes_to_ip(&net.mask)
                .map(|ip| ip.to_string())
                .unwrap_or_else(|_| "?".to_string());
            println!("    Net: {}/{}", addr, mask);
        }
        for pr in &src.ports {
            if pr.from == pr.to {
                println!("    Port: {}", pr.from);
            } else {
                println!("    Ports: {}-{}", pr.from, pr.to);
            }
        }
    }
}

fn print_vs_peers(vs: &balancerpb::VsState) {
    if vs.peers.is_empty() {
        return;
    }
    println!("  Peers:");
    for peer in &vs.peers {
        if let Ok(ip) = bytes_to_ip(peer) {
            println!("    {}", ip);
        }
    }
}

fn print_decap(state: &balancerpb::BalancerState) {
    if !state.source_ipv4.is_empty() {
        if let Ok(ip) = bytes_to_ip(&state.source_ipv4) {
            println!("Source IPv4: {}", ip);
        }
    }
    if !state.source_ipv6.is_empty() {
        if let Ok(ip) = bytes_to_ip(&state.source_ipv6) {
            println!("Source IPv6: {}", ip);
        }
    }
    if !state.decap_addresses.is_empty() {
        println!("Decap Addresses:");
        for addr in &state.decap_addresses {
            if let Ok(ip) = bytes_to_ip(addr) {
                println!("  {}", ip);
            }
        }
    }
    println!();
}

enum RealTableRow {
    Basic(RealBasicRow),
    Stats(RealStatsRow),
}

#[derive(Tabled)]
struct RealBasicRow {
    #[tabled(rename = "Real")]
    real: String,
    #[tabled(rename = "Enabled")]
    enabled: String,
    #[tabled(rename = "Wght")]
    weight: String,
    #[tabled(rename = "Eff Wght")]
    effective_weight: String,
}

// ─── Sessions Output ────────────────────────────────────────────────────────

pub fn print_sessions_header() {
    println!(
        "{:<40} {:<40} {:<50} {:<8} {:<8} {:<8}",
        "VS", "Real", "Client", "Expires", "Timeout", "Age"
    );
}

pub fn print_session(session: &balancerpb::Session) {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;

    let vs = format_vs_id(session.vs_id.as_ref());
    let real = format_real_id(session.real_id.as_ref());
    let client = format_client(session);
    let expires = format_expires(session, now);
    let age = format_age(session, now);
    let timeout = format_timeout(session);

    println!(
        "{:<40} {:<40} {:<50} {:<8} {:<8} {:<8}",
        vs, real, client, expires, timeout, age
    );
}

fn format_vs_id(vs_id: Option<&balancerpb::VsIdentifier>) -> String {
    vs_id
        .and_then(|id| {
            bytes_to_ip(&id.addr)
                .ok()
                .map(|ip| format!("{}/{}", format_ip_port(ip, id.port), proto_str(id.proto)))
        })
        .unwrap_or_else(|| "-".to_string())
}

fn format_real_id(real_id: Option<&balancerpb::RealIdentifier>) -> String {
    real_id
        .and_then(|id| {
            id.real
                .as_ref()
                .and_then(|r| bytes_to_ip(&r.ip).ok().map(|ip| format_ip_port(ip, r.port)))
        })
        .unwrap_or_else(|| "-".to_string())
}

fn format_client(session: &balancerpb::Session) -> String {
    bytes_to_ip(&session.client_addr)
        .ok()
        .map(|ip| format_ip_port(ip, session.client_port))
        .unwrap_or_else(|| "-".to_string())
}

fn format_expires(session: &balancerpb::Session, now: i64) -> String {
    match (session.last_packet_timestamp.as_ref(), session.timeout.as_ref()) {
        (Some(last_packet), Some(timeout)) => {
            let remaining = (last_packet.seconds + timeout.seconds - now).max(0);
            format!("{}", remaining)
        }
        _ => "-".to_string(),
    }
}

fn format_timeout(session: &balancerpb::Session) -> String {
    session
        .timeout
        .as_ref()
        .map_or_else(|| "-".to_string(), |d| format!("{}", d.seconds))
}

fn format_age(session: &balancerpb::Session, now: i64) -> String {
    session
        .create_timestamp
        .as_ref()
        .map_or_else(|| "-".to_string(), |ts| format!("{}", (now - ts.seconds).max(0)))
}

// ─── Tabled Row Types ───────────────────────────────────────────────────────

#[derive(Tabled)]
struct StatsRow {
    #[tabled(rename = "Category")]
    category: String,
    #[tabled(rename = "Metric")]
    metric: String,
    #[tabled(rename = "Value")]
    value: String,
}

impl StatsRow {
    fn new(category: &str, metric: &str, value: String) -> Self {
        Self {
            category: category.to_string(),
            metric: metric.to_string(),
            value,
        }
    }

    fn empty() -> Self {
        Self {
            category: String::new(),
            metric: String::new(),
            value: String::new(),
        }
    }
}

#[derive(Tabled)]
struct RealStatsRow {
    #[tabled(rename = "Real")]
    real: String,
    #[tabled(rename = "Enabled")]
    enabled: String,
    #[tabled(rename = "Wght")]
    weight: String,
    #[tabled(rename = "Eff Wght")]
    effective_weight: String,
    #[tabled(rename = "Pkts")]
    packets: String,
    #[tabled(rename = "Bytes")]
    bytes: String,
    #[tabled(rename = "Last Pkt")]
    last_packet: String,
    #[tabled(rename = "Dis Pkts")]
    disabled_pkts: String,
    #[tabled(rename = "ICMP Err")]
    icmp_pkts: String,
    #[tabled(rename = "Sess Act")]
    active_sessions: String,
    #[tabled(rename = "Sess Crt")]
    created_sessions: String,
}

// ─── Table Printing ─────────────────────────────────────────────────────────

fn print_table<T: Tabled>(entries: Vec<T>) {
    let mut table = Table::new(entries);
    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_horizontal(),
    );
    table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
    table.modify(Rows::first(), Color::BOLD);

    println!("{table}");
}

// ─── JSON Prettification ────────────────────────────────────────────────────

/// Recursively walk a JSON value and prettify it for human-readable output:
/// - Convert byte arrays (IP addresses) into IP strings.
/// - Convert known enum integer values into short string names.
pub fn prettify_json(value: &mut serde_json::Value) {
    match value {
        serde_json::Value::Array(arr) => {
            if let Some(ip) = try_bytes_to_ip_string(arr) {
                *value = serde_json::Value::String(ip);
            } else {
                for item in arr.iter_mut() {
                    prettify_json(item);
                }
            }
        }
        serde_json::Value::Object(map) => {
            prettify_enum(map, "scheduler", |v| {
                balancerpb::VsScheduler::try_from(v)
                    .ok()
                    .map(|s| s as i32)
                    .map(scheduler_str)
            });
            prettify_enum(map, "proto", |v| {
                balancerpb::TransportProto::try_from(v).ok().map(|p| match p {
                    balancerpb::TransportProto::Tcp => "tcp",
                    balancerpb::TransportProto::Udp => "udp",
                })
            });
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
    to_str: impl FnOnce(i32) -> Option<&'static str>,
) {
    if let Some(val) = map.get(key).and_then(|v| v.as_i64()) {
        if let Some(name) = to_str(val as i32) {
            map.insert(key.to_string(), serde_json::Value::String(name.to_string()));
        }
    }
}

fn try_bytes_to_ip_string(arr: &[serde_json::Value]) -> Option<String> {
    if arr.len() != 4 && arr.len() != 16 {
        return None;
    }
    let bytes: Vec<u8> = arr
        .iter()
        .map(|v| v.as_u64().and_then(|n| u8::try_from(n).ok()))
        .collect::<Option<Vec<_>>>()?;

    let ip = crate::bytes_to_ip(&bytes).ok()?;
    Some(ip.to_string())
}

// ─── Helpers ────────────────────────────────────────────────────────────────

fn proto_str(proto: i32) -> &'static str {
    match balancerpb::TransportProto::try_from(proto) {
        Ok(balancerpb::TransportProto::Tcp) => "TCP",
        Ok(balancerpb::TransportProto::Udp) => "UDP",
        _ => "???",
    }
}

fn scheduler_str(scheduler: i32) -> &'static str {
    match balancerpb::VsScheduler::try_from(scheduler) {
        Ok(balancerpb::VsScheduler::Sh) => "sh",
        Ok(balancerpb::VsScheduler::Wrr) => "wrr",
        Ok(balancerpb::VsScheduler::Wlc) => "wlc",
        _ => "???",
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
    if f.ops {
        parts.push("ops");
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

pub fn format_number(n: u64) -> String {
    n.to_string()
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
