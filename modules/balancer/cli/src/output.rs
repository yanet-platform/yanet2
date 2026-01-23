//! Output formatting for different display formats (JSON, Tree, Table)

use std::{error::Error, net::IpAddr};

use chrono::{DateTime, Utc};
use colored::Colorize;
use ptree::TreeBuilder;
use tabled::{Table, Tabled, settings::Style};

use crate::{
    entities::{addr_to_ip, format_bytes, format_number, opt_addr_to_ip},
    json_output,
    rpc::balancerpb,
};

////////////////////////////////////////////////////////////////////////////////
// Output Format Enum
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Copy)]
pub enum OutputFormat {
    Json,
    Tree,
    Table,
}

////////////////////////////////////////////////////////////////////////////////
// Helper Functions
////////////////////////////////////////////////////////////////////////////////

// fn format_vs(ip: String, port: u16, proto: i32) -> String {
//     if port == 0 {
//         format!("{}/{}", ip, proto_to_string(proto))
//     } else {
//         format!("{}:{}/{}", ip, port, proto_to_string(proto))
//     }
// }

fn format_real(ip: IpAddr, port: u16) -> String {
    if port == 0 {
        format!("{}", ip)
    } else {
        format!("{}:{}", ip, port)
    }
}

/// Print a boxed header with title and optional subtitle
fn print_boxed_header(title: &str, subtitle: Option<&str>) {
    let title_len = title.len();
    let subtitle_len = subtitle.map(|s| s.len()).unwrap_or(0);
    let max_len = title_len.max(subtitle_len);
    let box_width = max_len + 4; // 2 spaces padding on each side

    // Top border
    println!("{}", format!("╔{}╗", "═".repeat(box_width)).cyan().bold());

    // Title line (centered)
    let title_padding = (box_width - title_len) / 2;
    print!("{}", "║".cyan().bold());
    print!("{}", " ".repeat(title_padding));
    print!("{}", title.white().bold());
    print!("{}", " ".repeat(box_width - title_len - title_padding));
    println!("{}", "║".cyan().bold());

    // Subtitle line if present
    if let Some(sub) = subtitle {
        println!("{}", format!("╟{}╢", "─".repeat(box_width)).cyan());
        let sub_padding = (box_width - subtitle_len) / 2;
        print!("{}", "║".cyan());
        print!("{}", " ".repeat(sub_padding));
        print!("{}", sub.bright_white());
        print!("{}", " ".repeat(box_width - subtitle_len - sub_padding));
        println!("{}", "║".cyan());
    }

    // Bottom border
    println!("{}", format!("╚{}╝", "═".repeat(box_width)).cyan().bold());
}

fn proto_to_string(proto: i32) -> String {
    match balancerpb::TransportProto::try_from(proto) {
        Ok(balancerpb::TransportProto::Tcp) => "TCP".to_string(),
        Ok(balancerpb::TransportProto::Udp) => "UDP".to_string(),
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

fn format_timestamp(ts: Option<&prost_types::Timestamp>) -> String {
    match ts {
        Some(ts) if ts.seconds == 0 && ts.nanos == 0 => "N/A".to_string(),
        Some(ts) => {
            let dt = DateTime::<Utc>::from_timestamp(ts.seconds, ts.nanos as u32).unwrap_or_default();
            dt.format("%Y-%m-%d %H:%M:%S").to_string()
        }
        None => "N/A".to_string(),
    }
}

fn format_duration(dur: Option<&prost_types::Duration>) -> String {
    match dur {
        Some(dur) => format!("{}s", dur.seconds),
        None => "N/A".to_string(),
    }
}

fn format_flags(flags: Option<&balancerpb::VsFlags>) -> String {
    match flags {
        Some(flags) => {
            let mut parts = Vec::new();
            if flags.gre {
                parts.push("gre");
            }
            if flags.fix_mss {
                parts.push("mss");
            }
            if flags.ops {
                parts.push("ops");
            }
            if flags.pure_l3 {
                parts.push("l3");
            }
            if flags.wlc {
                parts.push("wlc");
            }
            if parts.is_empty() {
                "none".to_string()
            } else {
                parts.join(",")
            }
        }
        None => "none".to_string(),
    }
}

////////////////////////////////////////////////////////////////////////////////
// ShowConfig Output
////////////////////////////////////////////////////////////////////////////////

pub fn print_show_config(
    response: &balancerpb::ShowConfigResponse,
    format: OutputFormat,
) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => print_show_config_json(response),
        OutputFormat::Tree => print_show_config_tree(response),
        OutputFormat::Table => print_show_config_table(response),
    }
}

fn print_show_config_json(response: &balancerpb::ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let json = json_output::convert_show_config(response);
    println!("{}", serde_json::to_string_pretty(&json)?);
    Ok(())
}

fn print_show_config_tree(response: &balancerpb::ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Balancer Configuration".to_string());

    if let Some(config) = &response.config {
        if let Some(packet_handler) = &config.packet_handler {
            tree.begin_child("Packet Handler Config".to_string());

            // Source addresses
            tree.begin_child("Source Addresses".to_string());
            if let Ok(ipv4) = opt_addr_to_ip(&packet_handler.source_address_v4) {
                tree.add_empty_child(format!("IPv4: {}", ipv4));
            }
            if let Ok(ipv6) = opt_addr_to_ip(&packet_handler.source_address_v6) {
                tree.add_empty_child(format!("IPv6: {}", ipv6));
            }
            tree.end_child();

            // Decap addresses
            if !packet_handler.decap_addresses.is_empty() {
                tree.begin_child("Decap Addresses".to_string());
                for addr in &packet_handler.decap_addresses {
                    if let Ok(ip) = addr_to_ip(addr) {
                        tree.add_empty_child(ip.to_string());
                    }
                }
                tree.end_child();
            }

            // Timeouts
            if let Some(timeouts) = &packet_handler.sessions_timeouts {
                tree.begin_child("Session Timeouts".to_string());
                tree.add_empty_child(format!("TCP: {}s", timeouts.tcp));
                tree.add_empty_child(format!("TCP SYN: {}s", timeouts.tcp_syn));
                tree.add_empty_child(format!("TCP SYN-ACK: {}s", timeouts.tcp_syn_ack));
                tree.add_empty_child(format!("TCP FIN: {}s", timeouts.tcp_fin));
                tree.add_empty_child(format!("UDP: {}s", timeouts.udp));
                tree.add_empty_child(format!("Default: {}s", timeouts.default));
                tree.end_child();
            }

            // Virtual services
            tree.begin_child(format!("Virtual Services ({})", packet_handler.vs.len()));
            for (idx, vs) in packet_handler.vs.iter().enumerate() {
                if let Some(vs_id) = &vs.id {
                    if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                        tree.begin_child(format!(
                            "[{}] {}:{}/{}",
                            idx,
                            ip,
                            vs_id.port,
                            proto_to_string(vs_id.proto)
                        ));
                        tree.add_empty_child(format!("Scheduler: {}", scheduler_to_string(vs.scheduler)));
                        tree.add_empty_child(format!("Flags: {}", format_flags(vs.flags.as_ref())));

                        if !vs.allowed_srcs.is_empty() {
                            tree.begin_child("Allowed Sources".to_string());
                            for subnet in &vs.allowed_srcs {
                                if let Ok(ip) = opt_addr_to_ip(&subnet.addr) {
                                    tree.add_empty_child(format!("{}/{}", ip, subnet.size));
                                }
                            }
                            tree.end_child();
                        }

                        if !vs.peers.is_empty() {
                            tree.begin_child("Peers".to_string());
                            for peer in &vs.peers {
                                if let Ok(ip) = addr_to_ip(peer) {
                                    tree.add_empty_child(ip.to_string());
                                }
                            }
                            tree.end_child();
                        }

                        tree.begin_child("Reals".to_string());
                        for (ridx, real) in vs.reals.iter().enumerate() {
                            if let Some(real_id) = &real.id {
                                if let Ok(dst) = opt_addr_to_ip(&real_id.ip) {
                                    tree.add_empty_child(format!("[{}] {} weight={}", ridx, dst, real.weight));
                                }
                            }
                        }
                        tree.end_child();

                        tree.end_child();
                    }
                }
            }
            tree.end_child();

            tree.end_child();
        }

        if let Some(state_config) = &config.state {
            tree.begin_child("State Config".to_string());
            if let Some(capacity) = state_config.session_table_capacity {
                tree.add_empty_child(format!("Session Table Capacity: {}", format_number(capacity)));
            }
            if let Some(period) = &state_config.refresh_period {
                tree.add_empty_child(format!(
                    "Refresh Period: {}ms",
                    period.seconds * 1000 + period.nanos as i64 / 1_000_000
                ));
            }
            if let Some(load_factor) = state_config.session_table_max_load_factor {
                tree.add_empty_child(format!("Max Load Factor: {:.2}", load_factor));
            }
            if let Some(wlc) = &state_config.wlc {
                tree.begin_child("WLC Config".to_string());
                if let Some(power) = wlc.power {
                    tree.add_empty_child(format!("Power: {}", power));
                }
                if let Some(max_weight) = wlc.max_weight {
                    tree.add_empty_child(format!("Max Weight: {}", max_weight));
                }
                tree.end_child();
            }
            tree.end_child();
        }
    }

    if !response.buffered_real_updates.is_empty() {
        tree.begin_child(format!(
            "Buffered Real Updates ({})",
            response.buffered_real_updates.len()
        ));
        for update in &response.buffered_real_updates {
            if let Some(real_id) = &update.real_id {
                if let (Some(vs_id), Some(rel_real)) = (&real_id.vs, &real_id.real) {
                    let vip = opt_addr_to_ip(&vs_id.addr)
                        .ok()
                        .map(|ip| ip.to_string())
                        .unwrap_or_default();
                    let rip = opt_addr_to_ip(&rel_real.ip)
                        .ok()
                        .map(|ip| ip.to_string())
                        .unwrap_or_default();
                    let action = if update.enable.unwrap_or(false) {
                        "enable"
                    } else {
                        "disable"
                    };
                    tree.add_empty_child(format!(
                        "{} {}:{}/{} -> {}",
                        action,
                        vip,
                        vs_id.port,
                        proto_to_string(vs_id.proto),
                        rip
                    ));
                }
            }
        }
        tree.end_child();
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_show_config_table(response: &balancerpb::ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    print_boxed_header("BALANCER CONFIGURATION", None);
    println!();

    if let Some(config) = &response.config {
        if let Some(packet_handler) = &config.packet_handler {
            // Source addresses
            println!("{}", "Source Addresses:".bright_cyan().bold());
            if let Ok(ipv4) = opt_addr_to_ip(&packet_handler.source_address_v4) {
                println!("  IPv4: {}", ipv4.to_string().bright_green());
            }
            if let Ok(ipv6) = opt_addr_to_ip(&packet_handler.source_address_v6) {
                println!("  IPv6: {}", ipv6.to_string().bright_green());
            }

            println!("{}", "Decap Addresses:".bright_cyan().bold());
            if !packet_handler.decap_addresses.is_empty() {
                let decap_ips: Vec<String> = packet_handler
                    .decap_addresses
                    .iter()
                    .filter_map(|addr| addr_to_ip(addr).ok().map(|ip| ip.to_string()))
                    .collect();
                println!("  {}", decap_ips.join(", ").bright_green());
            } else {
                println!("  {}", "None".bright_green());
            }

            println!();

            // Timeouts
            if let Some(timeouts) = &packet_handler.sessions_timeouts {
                println!("{}", "Session Timeouts:".bright_cyan().bold());
                println!(
                    "  TCP: {}s | TCP SYN: {}s | TCP SYN-ACK: {}s | TCP FIN: {}s | UDP: {}s",
                    timeouts.tcp.to_string().bright_green(),
                    timeouts.tcp_syn.to_string().bright_green(),
                    timeouts.tcp_syn_ack.to_string().bright_green(),
                    timeouts.tcp_fin.to_string().bright_green(),
                    timeouts.udp.to_string().bright_green()
                );
                println!();
            }
        }

        // State Config
        if let Some(state_config) = &config.state {
            println!("{}", "State Config:".bright_cyan().bold());
            let refresh_period_ms = state_config
                .refresh_period
                .as_ref()
                .map(|p| (p.seconds * 1000 + p.nanos as i64 / 1_000_000).to_string())
                .unwrap_or_else(|| "N/A".to_string());

            let capacity_str = state_config
                .session_table_capacity
                .map(format_number)
                .unwrap_or_else(|| "N/A".to_string());

            let load_factor_str = state_config
                .session_table_max_load_factor
                .map(|lf| format!("{:.2}", lf))
                .unwrap_or_else(|| "N/A".to_string());

            println!(
                "  Session Table Capacity: {} | Refresh Period: {}ms | Max Load Factor: {}",
                capacity_str.bright_green(),
                refresh_period_ms.bright_green(),
                load_factor_str.bright_green()
            );

            if let Some(wlc) = &state_config.wlc {
                if let (Some(power), Some(max_weight)) = (wlc.power, wlc.max_weight) {
                    println!(
                        "  WLC Power={} | Max Weight={}",
                        power.to_string().bright_green(),
                        max_weight.to_string().bright_green()
                    );
                }
            }
            println!();
        }
    }

    // Virtual services table
    if let Some(config) = &response.config {
        if let Some(packet_handler) = &config.packet_handler {
            if !packet_handler.vs.is_empty() {
                println!("{}", "Virtual Services:".bright_yellow().bold());

                #[derive(Tabled)]
                struct VsRow {
                    #[tabled(rename = "Virtual IP")]
                    ip: String,
                    #[tabled(rename = "Port")]
                    port: String,
                    #[tabled(rename = "Proto")]
                    proto: String,
                    #[tabled(rename = "Scheduler")]
                    scheduler: String,
                    #[tabled(rename = "Flags")]
                    flags: String,
                    #[tabled(rename = "Reals")]
                    reals: String,
                }

                let rows: Vec<VsRow> = packet_handler
                    .vs
                    .iter()
                    .filter_map(|vs| {
                        vs.id.as_ref().map(|vs_id| VsRow {
                            ip: opt_addr_to_ip(&vs_id.addr).map(|ip| ip.to_string()).unwrap_or_default(),
                            port: vs_id.port.to_string(),
                            proto: proto_to_string(vs_id.proto),
                            scheduler: scheduler_to_string(vs.scheduler),
                            flags: format_flags(vs.flags.as_ref()),
                            reals: vs.reals.len().to_string(),
                        })
                    })
                    .collect();

                let table = Table::new(rows).with(Style::rounded()).to_string();
                println!("{}", table);
                println!();

                // Print details for each VS
                for vs in &packet_handler.vs {
                    if let Some(vs_id) = &vs.id {
                        if let Ok(vs_ip) = opt_addr_to_ip(&vs_id.addr) {
                            println!(
                                "{}:",
                                format!("VS {}:{}/{}", vs_ip, vs_id.port, proto_to_string(vs_id.proto))
                                    .bright_yellow()
                                    .bold()
                            );

                            #[derive(Tabled)]
                            struct RealRow {
                                #[tabled(rename = "Real IP")]
                                ip: String,
                                #[tabled(rename = "Real port")]
                                port: u16,
                                #[tabled(rename = "Weight")]
                                weight: String,
                                #[tabled(rename = "Source")]
                                source: String,
                                #[tabled(rename = "Source Mask")]
                                mask: String,
                            }

                            let real_rows: Vec<RealRow> = vs
                                .reals
                                .iter()
                                .filter_map(|real| {
                                    real.id.as_ref().map(|real_id| RealRow {
                                        ip: opt_addr_to_ip(&real_id.ip).map(|ip| ip.to_string()).unwrap_or_default(),
                                        port: real_id.port as u16,
                                        weight: real.weight.to_string(),
                                        source: opt_addr_to_ip(&real.src_addr)
                                            .map(|ip| ip.to_string())
                                            .unwrap_or_default(),
                                        mask: opt_addr_to_ip(&real.src_mask)
                                            .map(|ip| ip.to_string())
                                            .unwrap_or_default(),
                                    })
                                })
                                .collect();

                            let real_table = Table::new(real_rows).with(Style::rounded()).to_string();
                            println!("{}", real_table);

                            // Peers
                            if !vs.peers.is_empty() {
                                let peer_ips: Vec<String> = vs
                                    .peers
                                    .iter()
                                    .filter_map(|p| addr_to_ip(p).ok().map(|ip| ip.to_string()))
                                    .collect();
                                println!("{}: {}", "Peers".bright_cyan(), peer_ips.join(", "));
                            } else {
                                println!("{}: {}", "Peers".bright_cyan(), "none");
                            }

                            // Allowed sources
                            if !vs.allowed_srcs.is_empty() {
                                let srcs: Vec<String> = vs
                                    .allowed_srcs
                                    .iter()
                                    .filter_map(|s| opt_addr_to_ip(&s.addr).ok().map(|ip| format!("{}/{}", ip, s.size)))
                                    .collect();
                                println!("{}: {}", "Allowed Sources".bright_cyan(), srcs.join(", "));
                            } else {
                                println!("{}: {}", "Allowed Sources".bright_cyan(), "none");
                            }

                            println!();
                        }
                    }
                }
            }
        }
    }

    Ok(())
}

////////////////////////////////////////////////////////////////////////////////
// ListConfigs Output
////////////////////////////////////////////////////////////////////////////////

pub fn print_list_configs(
    response: &balancerpb::ListConfigsResponse,
    format: OutputFormat,
) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => {
            let json = json_output::convert_list_configs(response);
            println!("{}", serde_json::to_string_pretty(&json)?);
        }
        OutputFormat::Tree => {
            let mut tree = TreeBuilder::new(format!("Balancer Configs ({})", response.configs.len()));

            for config_name in &response.configs {
                tree.add_empty_child(config_name.clone());
            }

            let tree = tree.build();
            ptree::print_tree(&tree)?;
        }
        OutputFormat::Table => {
            #[derive(Tabled)]
            struct ConfigRow {
                #[tabled(rename = "Config Name")]
                name: String,
            }

            let rows: Vec<ConfigRow> = response
                .configs
                .iter()
                .map(|name| ConfigRow { name: name.clone() })
                .collect();

            let table = Table::new(rows).with(Style::rounded()).to_string();
            println!("{}", table);
        }
    }
    Ok(())
}

////////////////////////////////////////////////////////////////////////////////
// ShowInfo Output (Hierarchical)
////////////////////////////////////////////////////////////////////////////////

pub fn print_show_info(response: &balancerpb::ShowInfoResponse, format: OutputFormat) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => {
            let json = json_output::convert_show_info(response);
            println!("{}", serde_json::to_string_pretty(&json)?);
        }
        OutputFormat::Tree => print_show_info_tree(response)?,
        OutputFormat::Table => print_show_info_table(response)?,
    }
    Ok(())
}

fn print_show_info_tree(response: &balancerpb::ShowInfoResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Balancer State Info".to_string());

    tree.begin_child(format!("Config: {}", response.name));

    if let Some(info) = &response.info {
        tree.add_empty_child(format!("Active Sessions: {}", format_number(info.active_sessions)));
        tree.add_empty_child(format!(
            "Last Packet: {}",
            format_timestamp(info.last_packet_timestamp.as_ref())
        ));

        // Virtual services (hierarchical - reals nested under VS)
        if !info.vs.is_empty() {
            tree.begin_child(format!("Virtual Services ({})", info.vs.len()));
            for vs_info in &info.vs {
                if let Some(vs_id) = &vs_info.id {
                    if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                        tree.begin_child(format!("{}:{}/{}", ip, vs_id.port, proto_to_string(vs_id.proto)));
                        tree.add_empty_child(format!("Active Sessions: {}", format_number(vs_info.active_sessions)));
                        tree.add_empty_child(format!(
                            "Last Packet: {}",
                            format_timestamp(vs_info.last_packet_timestamp.as_ref())
                        ));

                        // Reals under this VS
                        if !vs_info.reals.is_empty() {
                            tree.begin_child(format!("Reals ({})", vs_info.reals.len()));
                            for real_info in &vs_info.reals {
                                if let Some(real_id) = &real_info.id {
                                    if let Some(rel_real) = &real_id.real {
                                        if let Ok(real_ip) = opt_addr_to_ip(&rel_real.ip) {
                                            tree.begin_child(real_ip.to_string());
                                            tree.add_empty_child(format!(
                                                "Active Sessions: {}",
                                                format_number(real_info.active_sessions)
                                            ));
                                            tree.add_empty_child(format!(
                                                "Last Packet: {}",
                                                format_timestamp(real_info.last_packet_timestamp.as_ref())
                                            ));
                                            tree.end_child();
                                        }
                                    }
                                }
                            }
                            tree.end_child();
                        }

                        tree.end_child();
                    }
                }
            }
            tree.end_child();
        }
    }

    tree.end_child();

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_show_info_table(response: &balancerpb::ShowInfoResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    let subtitle = if let Some(info) = &response.info {
        Some(format!(
            "Active Sessions: {} | Last Packet: {}",
            format_number(info.active_sessions),
            format_timestamp(info.last_packet_timestamp.as_ref())
        ))
    } else {
        Some(format!("Config: {}", response.name))
    };
    print_boxed_header("BALANCER INFO", subtitle.as_deref());
    println!();

    if let Some(info) = &response.info {
        // VS table (hierarchical display - reals nested under VS)
        if !info.vs.is_empty() {
            for vs_info in &info.vs {
                if let Some(vs_id) = &vs_info.id {
                    if let Ok(vs_ip) = opt_addr_to_ip(&vs_id.addr) {
                        println!(
                            "{}:",
                            format!("VS {}:{}/{}", vs_ip, vs_id.port, proto_to_string(vs_id.proto))
                                .bright_yellow()
                                .bold()
                        );
                        println!(
                            "  Active Sessions: {} | Last Packet: {}",
                            format_number(vs_info.active_sessions).bright_green(),
                            format_timestamp(vs_info.last_packet_timestamp.as_ref()).bright_green()
                        );

                        if !vs_info.reals.is_empty() {
                            #[derive(Tabled)]
                            struct RealInfoRow {
                                #[tabled(rename = "Real")]
                                real: String,
                                #[tabled(rename = "Active Sessions")]
                                sessions: String,
                                #[tabled(rename = "Last Packet (UTC)")]
                                last_packet: String,
                            }

                            let rows: Vec<RealInfoRow> = vs_info
                                .reals
                                .iter()
                                .filter_map(|real_info| {
                                    real_info.id.as_ref().and_then(|real_id| {
                                        real_id.real.as_ref().and_then(|rel_real| {
                                            opt_addr_to_ip(&rel_real.ip).ok().map(|real_ip| RealInfoRow {
                                                real: format_real(real_ip, rel_real.port as u16),
                                                sessions: format_number(real_info.active_sessions),
                                                last_packet: format_timestamp(real_info.last_packet_timestamp.as_ref()),
                                            })
                                        })
                                    })
                                })
                                .collect();

                            let table = Table::new(rows).with(Style::rounded()).to_string();
                            println!("{}", table);
                        }
                        println!();
                    }
                }
            }
        }
    }

    Ok(())
}

////////////////////////////////////////////////////////////////////////////////
// ShowStats Output (Hierarchical)
////////////////////////////////////////////////////////////////////////////////

pub fn print_show_stats(response: &balancerpb::ShowStatsResponse, format: OutputFormat) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => {
            let json = json_output::convert_show_stats(response);
            println!("{}", serde_json::to_string_pretty(&json)?);
        }
        OutputFormat::Tree => print_show_stats_tree(response)?,
        OutputFormat::Table => print_show_stats_table(response)?,
    }
    Ok(())
}

fn print_show_stats_tree(response: &balancerpb::ShowStatsResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Balancer Statistics".to_string());

    // Topology reference
    if let Some(ref_info) = &response.r#ref {
        tree.begin_child("Topology".to_string());
        if let Some(device) = &ref_info.device {
            tree.add_empty_child(format!("Device: {}", device));
        }
        if let Some(pipeline) = &ref_info.pipeline {
            tree.add_empty_child(format!("Pipeline: {}", pipeline));
        }
        if let Some(function) = &ref_info.function {
            tree.add_empty_child(format!("Function: {}", function));
        }
        if let Some(chain) = &ref_info.chain {
            tree.add_empty_child(format!("Chain: {}", chain));
        }
        tree.end_child();
    }

    if let Some(stats) = &response.stats {
        // Module stats (split into components)
        tree.begin_child("Module Stats".to_string());

        if let Some(common) = &stats.common {
            tree.begin_child("Common".to_string());
            tree.add_empty_child(format!("Incoming Packets: {}", format_number(common.incoming_packets)));
            tree.add_empty_child(format!("Incoming Bytes: {}", format_bytes(common.incoming_bytes)));
            tree.add_empty_child(format!("Outgoing Packets: {}", format_number(common.outgoing_packets)));
            tree.add_empty_child(format!("Outgoing Bytes: {}", format_bytes(common.outgoing_bytes)));
            tree.add_empty_child(format!("Decap Successful: {}", format_number(common.decap_successful)));
            tree.add_empty_child(format!("Decap Failed: {}", format_number(common.decap_failed)));
            tree.end_child();
        }

        if let Some(l4) = &stats.l4 {
            tree.begin_child("L4".to_string());
            tree.add_empty_child(format!("Incoming Packets: {}", format_number(l4.incoming_packets)));
            tree.add_empty_child(format!("Outgoing Packets: {}", format_number(l4.outgoing_packets)));
            tree.add_empty_child(format!("Select VS Failed: {}", format_number(l4.select_vs_failed)));
            tree.add_empty_child(format!("Select Real Failed: {}", format_number(l4.select_real_failed)));
            tree.add_empty_child(format!("Invalid Packets: {}", format_number(l4.invalid_packets)));
            tree.end_child();
        }

        if let Some(icmpv4) = &stats.icmpv4 {
            tree.begin_child("ICMPv4".to_string());
            tree.add_empty_child(format!("Incoming Packets: {}", format_number(icmpv4.incoming_packets)));
            tree.add_empty_child(format!("Src Not Allowed: {}", format_number(icmpv4.src_not_allowed)));
            tree.add_empty_child(format!("Echo Responses: {}", format_number(icmpv4.echo_responses)));
            tree.add_empty_child(format!("Unrecognized VS: {}", format_number(icmpv4.unrecognized_vs)));
            tree.add_empty_child(format!("Forwarded: {}", format_number(icmpv4.forwarded_packets)));
            tree.add_empty_child(format!("Broadcasted: {}", format_number(icmpv4.broadcasted_packets)));
            tree.end_child();
        }

        if let Some(icmpv6) = &stats.icmpv6 {
            tree.begin_child("ICMPv6".to_string());
            tree.add_empty_child(format!("Incoming Packets: {}", format_number(icmpv6.incoming_packets)));
            tree.add_empty_child(format!("Src Not Allowed: {}", format_number(icmpv6.src_not_allowed)));
            tree.add_empty_child(format!("Echo Responses: {}", format_number(icmpv6.echo_responses)));
            tree.add_empty_child(format!("Unrecognized VS: {}", format_number(icmpv6.unrecognized_vs)));
            tree.add_empty_child(format!("Forwarded: {}", format_number(icmpv6.forwarded_packets)));
            tree.add_empty_child(format!("Broadcasted: {}", format_number(icmpv6.broadcasted_packets)));
            tree.end_child();
        }

        tree.end_child();

        // VS stats
        if !stats.vs.is_empty() {
            tree.begin_child(format!("VS Stats ({})", stats.vs.len()));
            for vs in &stats.vs {
                if let Some(vs_id) = &vs.vs {
                    if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                        tree.begin_child(format!("{}:{}/{}", ip, vs_id.port, proto_to_string(vs_id.proto)));
                        if let Some(s) = &vs.stats {
                            tree.add_empty_child(format!(
                                "Incoming: {} pkts, {}",
                                format_number(s.incoming_packets),
                                format_bytes(s.incoming_bytes)
                            ));
                            tree.add_empty_child(format!(
                                "Outgoing: {} pkts, {}",
                                format_number(s.outgoing_packets),
                                format_bytes(s.outgoing_bytes)
                            ));
                            tree.add_empty_child(format!("Created Sessions: {}", format_number(s.created_sessions)));
                            tree.add_empty_child(format!(
                                "Packet Src Not Allowed: {}",
                                format_number(s.packet_src_not_allowed)
                            ));
                            tree.add_empty_child(format!("No Reals: {}", format_number(s.no_reals)));
                            tree.add_empty_child(format!("OPS Packets: {}", format_number(s.ops_packets)));
                            tree.add_empty_child(format!(
                                "Session Table Overflow: {}",
                                format_number(s.session_table_overflow)
                            ));
                            tree.add_empty_child(format!("Echo ICMP Packets: {}", format_number(s.echo_icmp_packets)));
                            tree.add_empty_child(format!(
                                "Error ICMP Packets: {}",
                                format_number(s.error_icmp_packets)
                            ));
                            tree.add_empty_child(format!("Real Is Disabled: {}", format_number(s.real_is_disabled)));
                            tree.add_empty_child(format!("Real Is Removed: {}", format_number(s.real_is_removed)));
                            tree.add_empty_child(format!(
                                "Not Rescheduled Packets: {}",
                                format_number(s.not_rescheduled_packets)
                            ));
                            tree.add_empty_child(format!(
                                "Broadcasted ICMP Packets: {}",
                                format_number(s.broadcasted_icmp_packets)
                            ));
                        }

                        // Real stats
                        if !vs.reals.is_empty() {
                            tree.begin_child(format!("Real Stats ({})", vs.reals.len()));
                            for real in &vs.reals {
                                if let Some(real_id) = &real.real {
                                    if let Some(rel_real) = &real_id.real {
                                        if let Ok(real_ip) = opt_addr_to_ip(&rel_real.ip) {
                                            tree.begin_child(real_ip.to_string());
                                            if let Some(s) = &real.stats {
                                                tree.add_empty_child(format!("Packets: {}", format_number(s.packets)));
                                                tree.add_empty_child(format!("Bytes: {}", format_bytes(s.bytes)));
                                                tree.add_empty_child(format!(
                                                    "Created Sessions: {}",
                                                    format_number(s.created_sessions)
                                                ));
                                                tree.add_empty_child(format!(
                                                    "Packets Real Disabled: {}",
                                                    format_number(s.packets_real_disabled)
                                                ));
                                                tree.add_empty_child(format!(
                                                    "OPS Packets: {}",
                                                    format_number(s.ops_packets)
                                                ));
                                                tree.add_empty_child(format!(
                                                    "Error ICMP Packets: {}",
                                                    format_number(s.error_icmp_packets)
                                                ));
                                            }
                                            tree.end_child();
                                        }
                                    }
                                }
                            }
                            tree.end_child();
                        }

                        tree.end_child();
                    }
                }
            }
            tree.end_child();
        }
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_show_stats_table(response: &balancerpb::ShowStatsResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    let subtitle = response.r#ref.as_ref().map(|ref_info| {
        format!(
            "Device: {} | Pipeline: {} | Function: {} | Chain: {}",
            ref_info.device.as_deref().unwrap_or("N/A"),
            ref_info.pipeline.as_deref().unwrap_or("N/A"),
            ref_info.function.as_deref().unwrap_or("N/A"),
            ref_info.chain.as_deref().unwrap_or("N/A")
        )
    });
    print_boxed_header("BALANCER STATISTICS", subtitle.as_deref());
    println!();

    if let Some(stats) = &response.stats {
        println!("{}", "Module Stats:".bright_yellow().bold());

        #[derive(Tabled)]
        struct ModuleStatsRow {
            #[tabled(rename = "Category")]
            category: String,
            #[tabled(rename = "Metric")]
            metric: String,
            #[tabled(rename = "Value")]
            value: String,
        }

        let mut rows = Vec::new();

        if let Some(common) = &stats.common {
            rows.push(ModuleStatsRow {
                category: "Common".to_string(),
                metric: "Incoming Pkts".to_string(),
                value: format_number(common.incoming_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Incoming Bytes".to_string(),
                value: format_bytes(common.incoming_bytes),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Outgoing Pkts".to_string(),
                value: format_number(common.outgoing_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Outgoing Bytes".to_string(),
                value: format_bytes(common.outgoing_bytes),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Decap Success".to_string(),
                value: format_number(common.decap_successful),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Decap Failed".to_string(),
                value: format_number(common.decap_failed),
            });
        }

        if let Some(l4) = &stats.l4 {
            rows.push(ModuleStatsRow {
                category: "L4".to_string(),
                metric: "Incoming Pkts".to_string(),
                value: format_number(l4.incoming_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Outgoing Pkts".to_string(),
                value: format_number(l4.outgoing_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Select VS Fail".to_string(),
                value: format_number(l4.select_vs_failed),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Select Real Fail".to_string(),
                value: format_number(l4.select_real_failed),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Invalid Pkts".to_string(),
                value: format_number(l4.invalid_packets),
            });
        }

        if let Some(icmpv4) = &stats.icmpv4 {
            rows.push(ModuleStatsRow {
                category: "ICMPv4".to_string(),
                metric: "Incoming Pkts".to_string(),
                value: format_number(icmpv4.incoming_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Src Not Allowed".to_string(),
                value: format_number(icmpv4.src_not_allowed),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Echo Responses".to_string(),
                value: format_number(icmpv4.echo_responses),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Unrecognized VS".to_string(),
                value: format_number(icmpv4.unrecognized_vs),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Forwarded".to_string(),
                value: format_number(icmpv4.forwarded_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Broadcasted".to_string(),
                value: format_number(icmpv4.broadcasted_packets),
            });
        }

        if let Some(icmpv6) = &stats.icmpv6 {
            rows.push(ModuleStatsRow {
                category: "ICMPv6".to_string(),
                metric: "Incoming Pkts".to_string(),
                value: format_number(icmpv6.incoming_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Src Not Allowed".to_string(),
                value: format_number(icmpv6.src_not_allowed),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Echo Responses".to_string(),
                value: format_number(icmpv6.echo_responses),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Unrecognized VS".to_string(),
                value: format_number(icmpv6.unrecognized_vs),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Forwarded".to_string(),
                value: format_number(icmpv6.forwarded_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Broadcasted".to_string(),
                value: format_number(icmpv6.broadcasted_packets),
            });
        }

        let table = Table::new(rows).with(Style::rounded()).to_string();
        println!("{}", table);
        println!();

        // VS Stats
        if !stats.vs.is_empty() {
            println!("{}", "VS Stats:".bright_yellow().bold());

            #[derive(Tabled)]
            struct VsStatsRow {
                #[tabled(rename = "VS IP")]
                ip: String,
                #[tabled(rename = "VS Port")]
                port: String,
                #[tabled(rename = "Proto")]
                proto: String,
                #[tabled(rename = "Incoming")]
                incoming: String,
                #[tabled(rename = "Outgoing")]
                outgoing: String,
                #[tabled(rename = "Created Sessions")]
                sessions: String,
            }

            let rows: Vec<VsStatsRow> = stats
                .vs
                .iter()
                .filter_map(|vs| {
                    vs.vs.as_ref().map(|vs_id| {
                        let s = vs.stats.as_ref();
                        VsStatsRow {
                            ip: opt_addr_to_ip(&vs_id.addr).map(|ip| ip.to_string()).unwrap_or_default(),
                            port: vs_id.port.to_string(),
                            proto: proto_to_string(vs_id.proto),
                            incoming: s
                                .map(|s| {
                                    format!(
                                        "{} pkts, {}",
                                        format_number(s.incoming_packets),
                                        format_bytes(s.incoming_bytes)
                                    )
                                })
                                .unwrap_or_else(|| "0 pkts, 0 B".to_string()),
                            outgoing: s
                                .map(|s| {
                                    format!(
                                        "{} pkts, {}",
                                        format_number(s.outgoing_packets),
                                        format_bytes(s.outgoing_bytes)
                                    )
                                })
                                .unwrap_or_else(|| "0 pkts, 0 B".to_string()),
                            sessions: s
                                .map(|s| format_number(s.created_sessions))
                                .unwrap_or_else(|| "0".to_string()),
                        }
                    })
                })
                .collect();

            let table = Table::new(rows).with(Style::rounded()).to_string();
            println!("{}", table);
            println!();

            // Real Stats
            println!("{}", "Real Stats:".bright_yellow().bold());

            #[derive(Tabled)]
            struct RealStatsRow {
                #[tabled(rename = "VS IP")]
                vs_ip: String,
                #[tabled(rename = "VS Port")]
                vs_port: String,
                #[tabled(rename = "Real IP")]
                real_ip: String,
                #[tabled(rename = "Real Port")]
                real_port: String,
                #[tabled(rename = "Proto")]
                proto: String,
                #[tabled(rename = "Traffic")]
                traffic: String,
                #[tabled(rename = "Created Sessions")]
                sessions: String,
            }

            let mut real_rows = Vec::new();
            for vs in &stats.vs {
                if let Some(vs_id) = &vs.vs {
                    for real in &vs.reals {
                        if let Some(real_id) = &real.real {
                            if let Some(rel_real) = &real_id.real {
                                let s = real.stats.as_ref();
                                real_rows.push(RealStatsRow {
                                    vs_ip: opt_addr_to_ip(&vs_id.addr).map(|ip| ip.to_string()).unwrap_or_default(),
                                    vs_port: vs_id.port.to_string(),
                                    real_ip: opt_addr_to_ip(&rel_real.ip)
                                        .map(|ip| ip.to_string())
                                        .unwrap_or_default(),
                                    real_port: rel_real.port.to_string(),
                                    proto: proto_to_string(vs_id.proto),
                                    traffic: s
                                        .map(|s| {
                                            format!("{} pkts, {}", format_number(s.packets), format_bytes(s.bytes))
                                        })
                                        .unwrap_or_else(|| "0 pkts, 0 B".to_string()),
                                    sessions: s
                                        .map(|s| format_number(s.created_sessions))
                                        .unwrap_or_else(|| "0".to_string()),
                                });
                            }
                        }
                    }
                }
            }

            let table = Table::new(real_rows).with(Style::rounded()).to_string();
            println!("{}", table);
        }
    }

    Ok(())
}

////////////////////////////////////////////////////////////////////////////////
// ShowSessions Output
////////////////////////////////////////////////////////////////////////////////

pub fn print_show_sessions(
    response: &balancerpb::ShowSessionsResponse,
    format: OutputFormat,
) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => {
            let json = json_output::convert_show_sessions(response);
            println!("{}", serde_json::to_string_pretty(&json)?);
        }
        OutputFormat::Tree => print_show_sessions_tree(response)?,
        OutputFormat::Table => print_show_sessions_table(response)?,
    }
    Ok(())
}

fn print_show_sessions_tree(response: &balancerpb::ShowSessionsResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Active Sessions".to_string());

    tree.add_empty_child(format!(
        "Total Sessions: {}",
        format_number(response.sessions.len() as u64)
    ));

    for (idx, session) in response.sessions.iter().enumerate() {
        if let (Ok(client), Some(vs_id), Some(real_id)) =
            (opt_addr_to_ip(&session.client_addr), &session.vs_id, &session.real_id)
        {
            if let (Ok(vs_ip), Some(rel_real)) = (opt_addr_to_ip(&vs_id.addr), &real_id.real) {
                if let Ok(real_ip) = opt_addr_to_ip(&rel_real.ip) {
                    tree.begin_child(format!(
                        "[{}] {}:{} -> {}:{} -> {}:{}",
                        idx, client, session.client_port, vs_ip, vs_id.port, real_ip, rel_real.port
                    ));
                    tree.add_empty_child(format!(
                        "Created: {}",
                        format_timestamp(session.create_timestamp.as_ref())
                    ));
                    tree.add_empty_child(format!(
                        "Last Packet: {}",
                        format_timestamp(session.last_packet_timestamp.as_ref())
                    ));
                    tree.add_empty_child(format!("Timeout: {}", format_duration(session.timeout.as_ref())));
                    tree.end_child();
                }
            }
        }
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_show_sessions_table(response: &balancerpb::ShowSessionsResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    let subtitle = Some(format!(
        "Total Sessions: {}",
        format_number(response.sessions.len() as u64)
    ));
    print_boxed_header("ACTIVE SESSIONS", subtitle.as_deref());
    println!();

    if !response.sessions.is_empty() {
        #[derive(Tabled)]
        struct SessionRow {
            #[tabled(rename = "Client")]
            client: String,
            #[tabled(rename = "VS")]
            vs: String,
            #[tabled(rename = "Real")]
            real: String,
            #[tabled(rename = "Proto")]
            proto: String,
            #[tabled(rename = "Created At (UTC)")]
            created_at: String,
            #[tabled(rename = "Last Packet (UTC)")]
            last_packet: String,
            #[tabled(rename = "Timeout")]
            timeout: String,
        }

        let rows: Vec<SessionRow> = response
            .sessions
            .iter()
            .filter_map(|session| {
                if let (Ok(client_ip), Some(vs_id), Some(real_id)) =
                    (opt_addr_to_ip(&session.client_addr), &session.vs_id, &session.real_id)
                {
                    if let (Ok(vs_ip), Some(rel_real)) = (opt_addr_to_ip(&vs_id.addr), &real_id.real) {
                        if let Ok(real_ip) = opt_addr_to_ip(&rel_real.ip) {
                            return Some(SessionRow {
                                client: format!("{}:{}", client_ip, session.client_port),
                                vs: format!("{}:{}", vs_ip, vs_id.port),
                                real: format_real(real_ip, rel_real.port as u16),
                                proto: proto_to_string(vs_id.proto),
                                created_at: format_timestamp(session.create_timestamp.as_ref()),
                                last_packet: format_timestamp(session.last_packet_timestamp.as_ref()),
                                timeout: format_duration(session.timeout.as_ref()),
                            });
                        }
                    }
                }
                None
            })
            .collect();

        let table = Table::new(rows).with(Style::rounded()).to_string();

        println!("{}", table);
    }

    Ok(())
}

////////////////////////////////////////////////////////////////////////////////
// ShowGraph Output
////////////////////////////////////////////////////////////////////////////////

pub fn print_show_graph(response: &balancerpb::ShowGraphResponse, format: OutputFormat) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => {
            let json = json_output::convert_show_graph(response);
            println!("{}", serde_json::to_string_pretty(&json)?);
        }
        OutputFormat::Tree => print_show_graph_tree(response)?,
        OutputFormat::Table => {
            // Graph output doesn't support table format yet, fallback to tree
            println!("Table format not supported for graph, using tree");
            print_show_graph_tree(response)?;
        }
    }
    Ok(())
}

fn print_show_graph_tree(response: &balancerpb::ShowGraphResponse) -> Result<(), Box<dyn Error>> {
    let mut tree: TreeBuilder = TreeBuilder::new("Balancer Graph".to_string());

    if let Some(graph) = &response.graph {
        tree.add_empty_child(format!(
            "Virtual Services: {}",
            format_number(graph.virtual_services.len() as u64)
        ));

        for (idx, vs) in graph.virtual_services.iter().enumerate() {
            if let Some(vs_id) = &vs.identifier {
                if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                    tree.begin_child(format!(
                        "[{}] {}:{}/{}",
                        idx,
                        ip,
                        vs_id.port,
                        proto_to_string(vs_id.proto)
                    ));

                    for (ridx, real) in vs.reals.iter().enumerate() {
                        if let Some(real_id) = &real.identifier {
                            if let Ok(real_ip) = opt_addr_to_ip(&real_id.ip) {
                                let status = if real.enabled { "enabled" } else { "disabled" };
                                tree.add_empty_child(format!(
                                    "[{}] {} weight={} effective_weight={} ({})",
                                    ridx, real_ip, real.weight, real.effective_weight, status
                                ));
                            }
                        }
                    }
                    tree.end_child();
                }
            }
        }
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}
