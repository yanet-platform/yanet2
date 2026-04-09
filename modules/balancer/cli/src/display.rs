use tabled::{
    settings::{
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
        Color, Style,
    },
    Table, Tabled,
};

use yanet_cli_balancer::balancerpb;

use crate::{bytes_to_ip, format_ip_port};

// ─── Compact (IPVS-style) Output ────────────────────────────────────────────

pub fn print_compact(states: &[balancerpb::BalancerState]) {
    println!(
        "{:<5}{:<42}{:<6}Flags",
        "Prot", "LocalAddress:Port", "Scheduler"
    );
    println!(
        "  -> {:<40}{:>8}{:>10}{:>12}{:>10}",
        "RemoteAddress:Port", "Weight", "EfWeight", "ActiveConn", "Enabled"
    );

    for state in states {
        if !state.balancer_name.is_empty() {
            println!();
            println!("Balancer: {}", state.balancer_name);
            if let Some(r) = &state.r#ref {
                print_ref_inline(r);
            }
            println!("Active Sessions: {}", format_number(state.active_sessions));
        }
        println!();

        for vs in &state.virtual_services {
            let Some(id) = &vs.id else { continue };
            let ip = match bytes_to_ip(&id.addr) {
                Ok(ip) => ip,
                Err(_) => continue,
            };
            let proto = proto_str(id.proto);
            let scheduler = scheduler_str(vs.scheduler);
            let flags = flags_str(vs.flags.as_ref());
            let addr_port = format_ip_port(ip, id.port);

            if flags.is_empty() {
                println!("{:<5}{:<42}{}", proto, addr_port, scheduler);
            } else {
                println!("{:<5}{:<42}{} {}", proto, addr_port, scheduler, flags);
            }

            for real in &vs.reals {
                let Some(rid) = &real.id else { continue };
                let rip = match bytes_to_ip(&rid.ip) {
                    Ok(ip) => ip,
                    Err(_) => continue,
                };
                let real_addr = format_ip_port(rip, rid.port);
                let enabled = if real.enabled { "yes" } else { "no" };
                println!(
                    "  -> {:<40}{:>8}{:>10}{:>12}{:>10}",
                    real_addr,
                    format_number(real.weight),
                    format_number(real.effective_weight),
                    format_number(real.active_sessions),
                    enabled,
                );
            }
        }
    }
}

// ─── Detail Output ──────────────────────────────────────────────────────────

pub fn print_detail(states: &[balancerpb::BalancerState]) {
    for (i, state) in states.iter().enumerate() {
        if i > 0 {
            println!();
        }
        print_detail_header(state);
        print_module_stats(state);

        for vs in &state.virtual_services {
            print_vs_detail(vs);
        }
    }
}

fn print_detail_header(state: &balancerpb::BalancerState) {
    println!("Balancer: {}", state.balancer_name);
    if let Some(r) = &state.r#ref {
        print_ref_inline(r);
    }
    println!("Active Sessions: {}", format_number(state.active_sessions));
    if let Some(ts) = &state.last_packet_timestamp {
        println!("Last Packet: {}", format_timestamp(ts));
    }
    println!();
}

fn print_module_stats(state: &balancerpb::BalancerState) {
    println!("Module:");

    let mut rows: Vec<StatsRow> = Vec::new();

    if let Some(c) = &state.common_stats {
        rows.push(StatsRow::new("Common", "Incoming Pkts", format_number(c.incoming_packets)));
        rows.push(StatsRow::new("", "Incoming Bytes", format_bytes(c.incoming_bytes)));
        rows.push(StatsRow::new("", "Unexpected Proto", format_number(c.unexpected_network_proto)));
        rows.push(StatsRow::new("", "Decap Success", format_number(c.decap_successful)));
        rows.push(StatsRow::new("", "Decap Failed", format_number(c.decap_failed)));
        rows.push(StatsRow::new("", "Outgoing Pkts", format_number(c.outgoing_packets)));
        rows.push(StatsRow::new("", "Outgoing Bytes", format_bytes(c.outgoing_bytes)));
        rows.push(StatsRow::empty());
    }

    if let Some(l) = &state.l4_stats {
        rows.push(StatsRow::new("L4", "Incoming Pkts", format_number(l.incoming_packets)));
        rows.push(StatsRow::new("", "Outgoing Pkts", format_number(l.outgoing_packets)));
        rows.push(StatsRow::new("", "Select VS Fail", format_number(l.select_vs_failed)));
        rows.push(StatsRow::new("", "Select Real Fail", format_number(l.select_real_failed)));
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
    if rows.last().is_some_and(|r| r.category.is_empty() && r.metric.is_empty()) {
        rows.pop();
    }

    print_table(rows);
    println!();
}

fn push_icmp_rows(rows: &mut Vec<StatsRow>, category: &str, icmp: &balancerpb::IcmpStats) {
    rows.push(StatsRow::new(category, "Incoming Pkts", format_number(icmp.incoming_packets)));
    rows.push(StatsRow::new("", "Src Not Allowed", format_number(icmp.src_not_allowed)));
    rows.push(StatsRow::new("", "Echo Responses", format_number(icmp.echo_responses)));
    rows.push(StatsRow::new("", "Payload Short IP", format_number(icmp.payload_too_short_ip)));
    rows.push(StatsRow::new("", "Unmatch Src Orig", format_number(icmp.unmatching_src_from_original)));
    rows.push(StatsRow::new("", "Payload Short Port", format_number(icmp.payload_too_short_port)));
    rows.push(StatsRow::new("", "Unexpected Trans", format_number(icmp.unexpected_transport)));
    rows.push(StatsRow::new("", "Unrecognized VS", format_number(icmp.unrecognized_vs)));
    rows.push(StatsRow::new("", "Forwarded Pkts", format_number(icmp.forwarded_packets)));
    rows.push(StatsRow::new("", "Broadcasted Pkts", format_number(icmp.broadcasted_packets)));
    rows.push(StatsRow::new("", "Clones Sent", format_number(icmp.packet_clones_sent)));
    rows.push(StatsRow::new("", "Clones Received", format_number(icmp.packet_clones_received)));
    rows.push(StatsRow::new("", "Clone Failures", format_number(icmp.packet_clone_failures)));
}

fn print_vs_detail(vs: &balancerpb::VsState) {
    let Some(id) = &vs.id else { return };
    let ip = match bytes_to_ip(&id.addr) {
        Ok(ip) => ip,
        Err(_) => return,
    };
    let proto = proto_str(id.proto).to_uppercase();
    let addr_port = format_ip_port(ip, id.port);

    println!("VS {}/{}:", addr_port, proto);
    println!("  Active Sessions: {}", format_number(vs.active_sessions));
    if let Some(ts) = &vs.last_packet_timestamp {
        println!("  Last Packet: {}", format_timestamp(ts));
    }

    if let Some(stats) = &vs.stats {
        println!("  Incoming Packets: {}", format_number(stats.incoming_packets));
        println!("  Incoming Bytes: {}", format_bytes(stats.incoming_bytes));
        println!("  Outgoing Packets: {}", format_number(stats.outgoing_packets));
        println!("  Outgoing Bytes: {}", format_bytes(stats.outgoing_bytes));
        println!("  Created Sessions: {}", format_number(stats.created_sessions));
        println!("  Packet Src Not Allowed: {}", format_number(stats.packet_src_not_allowed));
        println!("  No Reals: {}", format_number(stats.no_reals));
        println!("  Session Table Overflow: {}", format_number(stats.session_table_overflow));
        println!("  Echo ICMP Packets: {}", format_number(stats.echo_icmp_packets));
        println!("  Error ICMP Packets: {}", format_number(stats.error_icmp_packets));
        println!("  Real Is Disabled: {}", format_number(stats.real_is_disabled));
        println!("  Real Is Removed: {}", format_number(stats.real_is_removed));
        println!("  Not Rescheduled Packets: {}", format_number(stats.not_rescheduled_packets));
        println!("  Broadcasted ICMP Packets: {}", format_number(stats.broadcasted_icmp_packets));
    }

    if !vs.allowed_sources.is_empty() {
        println!("  Allowed Sources:");
        for acl in &vs.allowed_sources {
            println!("    Tag {}: {}", acl.tag, format_number(acl.passes));
        }
    }

    let real_rows: Vec<RealStatsRow> = vs
        .reals
        .iter()
        .filter_map(|real| {
            let rid = real.id.as_ref()?;
            let rip = bytes_to_ip(&rid.ip).ok()?;
            let real_addr = format_ip_port(rip, rid.port);
            let rs = real.real_stats.as_ref();
            Some(RealStatsRow {
                real: real_addr,
                packets: format_number(rs.map_or(0, |s| s.packets)),
                bytes: format_bytes(rs.map_or(0, |s| s.bytes)),
                created_sessions: format_number(rs.map_or(0, |s| s.created_sessions)),
                active_sessions: format_number(real.active_sessions),
                last_packet: real
                    .last_packet_timestamp
                    .as_ref()
                    .map_or_else(|| "-".to_string(), format_timestamp),
                disabled_pkts: format_number(rs.map_or(0, |s| s.packets_real_disabled)),
                icmp_pkts: format_number(rs.map_or(0, |s| s.error_icmp_packets)),
            })
        })
        .collect();

    if !real_rows.is_empty() {
        print_table(real_rows);
    }
    println!();
}

// ─── Sessions Output ────────────────────────────────────────────────────────

pub fn print_sessions_header() {
    println!(
        "{:<5}{:<24}{:<20}{:<20}{:<8}{:<21}LastPacket",
        "Prot", "Client", "Virtual", "Real", "Timeout", "Created"
    );
}

pub fn print_session(session: &balancerpb::Session) {
    let vs_id = session.vs_id.as_ref();
    let real_id = session.real_id.as_ref();

    let proto = vs_id.map_or("-", |id| proto_str(id.proto));

    let client = match bytes_to_ip(&session.client_addr) {
        Ok(ip) => format_ip_port(ip, session.client_port),
        Err(_) => "-".to_string(),
    };

    let virtual_addr = vs_id
        .and_then(|id| bytes_to_ip(&id.addr).ok().map(|ip| format_ip_port(ip, id.port)))
        .unwrap_or_else(|| "-".to_string());

    let real_addr = real_id
        .and_then(|id| {
            id.real
                .as_ref()
                .and_then(|r| bytes_to_ip(&r.ip).ok().map(|ip| format_ip_port(ip, r.port)))
        })
        .unwrap_or_else(|| "-".to_string());

    let timeout = session
        .timeout
        .as_ref()
        .map_or_else(|| "-".to_string(), |d| format!("{}s", d.seconds));

    let created = session
        .create_timestamp
        .as_ref()
        .map_or_else(|| "-".to_string(), format_timestamp);

    let last_packet = session
        .last_packet_timestamp
        .as_ref()
        .map_or_else(|| "-".to_string(), format_timestamp);

    println!(
        "{:<5}{:<24}{:<20}{:<20}{:<8}{:<21}{}",
        proto, client, virtual_addr, real_addr, timeout, created, last_packet
    );
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
    #[tabled(rename = "Packets")]
    packets: String,
    #[tabled(rename = "Bytes")]
    bytes: String,
    #[tabled(rename = "Created Sessions")]
    created_sessions: String,
    #[tabled(rename = "Active Sessions")]
    active_sessions: String,
    #[tabled(rename = "Last Packet")]
    last_packet: String,
    #[tabled(rename = "Disabled Pkts")]
    disabled_pkts: String,
    #[tabled(rename = "ICMP Pkts")]
    icmp_pkts: String,
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
        Ok(balancerpb::VsScheduler::SourceHash) => "sh",
        Ok(balancerpb::VsScheduler::RoundRobin) => "rr",
        _ => "??",
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
    if f.wlc {
        parts.push("wlc");
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
    if n == 0 {
        return "0".to_string();
    }
    let s = n.to_string();
    let mut result = String::with_capacity(s.len() + s.len() / 3);
    for (i, c) in s.chars().rev().enumerate() {
        if i > 0 && i % 3 == 0 {
            result.push(',');
        }
        result.push(c);
    }
    result.chars().rev().collect()
}

fn format_bytes(bytes: u64) -> String {
    const KB: f64 = 1024.0;
    const MB: f64 = 1024.0 * 1024.0;
    const GB: f64 = 1024.0 * 1024.0 * 1024.0;
    const TB: f64 = 1024.0 * 1024.0 * 1024.0 * 1024.0;

    let b = bytes as f64;
    if b >= TB {
        format!("{:.1} TB", b / TB)
    } else if b >= GB {
        format!("{:.1} GB", b / GB)
    } else if b >= MB {
        format!("{:.1} MB", b / MB)
    } else if b >= KB {
        format!("{:.1} KB", b / KB)
    } else {
        format!("{} B", bytes)
    }
}

fn format_timestamp(ts: &prost_types::Timestamp) -> String {
    let secs = ts.seconds;
    let ndt = chrono::DateTime::from_timestamp(secs, ts.nanos as u32);
    match ndt {
        Some(dt) => dt.format("%Y-%m-%d %H:%M:%S").to_string(),
        None => "-".to_string(),
    }
}
