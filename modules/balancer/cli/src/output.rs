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

#[derive(Debug, Clone, Copy)]
pub enum InspectOutputFormat {
    Json,
    Normal,
    Detail,
}

////////////////////////////////////////////////////////////////////////////////
// Helper Functions
////////////////////////////////////////////////////////////////////////////////

fn format_real(ip: IpAddr, port: u16) -> String {
    if port == 0 {
        format!("{}", ip)
    } else {
        format_ip_port(ip, port)
    }
}

/// Format IP address with port, using brackets for IPv6
fn format_ip_port(ip: IpAddr, port: u16) -> String {
    match ip {
        IpAddr::V4(_) => format!("{}:{}", ip, port),
        IpAddr::V6(_) => format!("[{}]:{}", ip, port),
    }
}

/// Format IP address with port and protocol, using brackets for IPv6
fn format_vs(ip: IpAddr, port: u32, proto: i32) -> String {
    let proto_str = proto_to_string(proto);
    match ip {
        IpAddr::V4(_) => format!("{}:{}/{}", ip, port, proto_str),
        IpAddr::V6(_) => format!("[{}]:{}/{}", ip, port, proto_str),
    }
}

/// Print a boxed header with title and optional subtitle (can be multi-line)
fn print_boxed_header(title: &str, subtitle: Option<&str>) {
    let title_len = title.len();

    // Handle multi-line subtitles
    let subtitle_lines: Vec<&str> = subtitle.map(|s| s.lines().collect()).unwrap_or_default();
    let max_subtitle_len = subtitle_lines.iter().map(|line| line.len()).max().unwrap_or(0);
    let max_len = title_len.max(max_subtitle_len);
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

    // Subtitle lines if present
    if !subtitle_lines.is_empty() {
        println!("{}", format!("╟{}╢", "─".repeat(box_width)).cyan());
        for line in subtitle_lines {
            let line_len = line.len();
            let line_padding = (box_width - line_len) / 2;
            print!("{}", "║".cyan());
            print!("{}", " ".repeat(line_padding));
            print!("{}", line.bright_white());
            print!("{}", " ".repeat(box_width - line_len - line_padding));
            println!("{}", "║".cyan());
        }
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
// UpdateInfo Output
////////////////////////////////////////////////////////////////////////////////

/// Print update information after configuration update
pub fn print_update_info(update_info: &balancerpb::UpdateInfo, format: OutputFormat) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => print_update_info_json(update_info),
        OutputFormat::Tree => print_update_info_tree(update_info),
        OutputFormat::Table => print_update_info_table(update_info),
    }
}

fn print_update_info_json(update_info: &balancerpb::UpdateInfo) -> Result<(), Box<dyn Error>> {
    let json = json_output::convert_update_info(update_info);
    println!("{}", serde_json::to_string(&json)?);
    Ok(())
}

fn print_update_info_tree(update_info: &balancerpb::UpdateInfo) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Configuration Update".to_string());

    // Operation type
    let operation = if update_info.created {
        "Created (new configuration)"
    } else {
        "Updated (existing configuration)"
    };
    tree.add_empty_child(format!("Operation: {}", operation));

    // Filter reuse status (only relevant for updates)
    if !update_info.created {
        tree.begin_child("Filter Reuse Status".to_string());

        let ipv4_status = if update_info.vs_ipv4_matcher_reused {
            "Reused (not recompiled)"
        } else {
            "Recompiled"
        };
        tree.add_empty_child(format!("IPv4 VS Matcher: {}", ipv4_status));

        let ipv6_status = if update_info.vs_ipv6_matcher_reused {
            "Reused (not recompiled)"
        } else {
            "Recompiled"
        };
        tree.add_empty_child(format!("IPv6 VS Matcher: {}", ipv6_status));

        tree.end_child();

        // ACL reuse information
        if !update_info.vs_acl_reuses.is_empty() {
            tree.begin_child(format!(
                "ACL Filters Reused ({} virtual services)",
                update_info.vs_acl_reuses.len()
            ));
            for vs_id in &update_info.vs_acl_reuses {
                if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                    tree.add_empty_child(format_vs(ip, vs_id.port, vs_id.proto));
                }
            }
            tree.end_child();
        } else {
            tree.add_empty_child("ACL Filters Reused: None (all ACLs recompiled)".to_string());
        }
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_update_info_table(update_info: &balancerpb::UpdateInfo) -> Result<(), Box<dyn Error>> {
    println!();
    println!("{}", "═".repeat(60).cyan().bold());
    println!("{}", "  Configuration Update Summary".white().bold());
    println!("{}", "═".repeat(60).cyan().bold());
    println!();

    // Operation type
    let operation = if update_info.created {
        "Created (new configuration)".bright_green().bold()
    } else {
        "Updated (existing configuration)".bright_blue().bold()
    };
    println!("{} {}", "Operation:".bright_cyan().bold(), operation);
    println!();

    // Filter reuse status (only relevant for updates)
    if !update_info.created {
        println!("{}", "Filter Reuse Status:".bright_cyan().bold());

        let ipv4_status = if update_info.vs_ipv4_matcher_reused {
            "✓ Reused (not recompiled)".bright_green()
        } else {
            "✗ Recompiled".bright_yellow()
        };
        println!("  IPv4 VS Matcher: {}", ipv4_status);

        let ipv6_status = if update_info.vs_ipv6_matcher_reused {
            "✓ Reused (not recompiled)".bright_green()
        } else {
            "✗ Recompiled".bright_yellow()
        };
        println!("  IPv6 VS Matcher: {}", ipv6_status);

        println!();

        // ACL reuse information
        if !update_info.vs_acl_reuses.is_empty() {
            println!(
                "{} {}",
                "ACL Filters Reused:".bright_cyan().bold(),
                format!("({} virtual services)", update_info.vs_acl_reuses.len()).bright_white()
            );

            for vs_id in &update_info.vs_acl_reuses {
                if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                    println!("  • {}", format_vs(ip, vs_id.port, vs_id.proto).bright_yellow(),);
                }
            }
        } else {
            println!(
                "{} {}",
                "ACL Filters Reused:".bright_cyan().bold(),
                "None (all ACLs recompiled)".bright_yellow()
            );
        }

        println!();
    }

    println!("{}", "═".repeat(60).cyan().bold());
    println!();

    Ok(())
}

////////////////////////////////////////////////////////////////////////////////
// VS Update/Delete Info Output
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Copy)]
pub enum VsOperation {
    Update,
    Delete,
}

/// Print VS update/delete information (without created flag)
pub fn print_vs_update_info(
    update_info: &balancerpb::UpdateInfo,
    format: OutputFormat,
    operation: VsOperation,
) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => print_vs_update_info_json(update_info),
        OutputFormat::Tree => print_vs_update_info_tree(update_info, operation),
        OutputFormat::Table => print_vs_update_info_table(update_info, operation),
    }
}

fn print_vs_update_info_json(update_info: &balancerpb::UpdateInfo) -> Result<(), Box<dyn Error>> {
    let json = json_output::convert_vs_update_info(update_info);
    println!("{}", serde_json::to_string(&json)?);
    Ok(())
}

fn print_vs_update_info_tree(
    update_info: &balancerpb::UpdateInfo,
    operation: VsOperation,
) -> Result<(), Box<dyn Error>> {
    let title = match operation {
        VsOperation::Update => "VS Update Result",
        VsOperation::Delete => "VS Delete Result",
    };
    let mut tree = TreeBuilder::new(title.to_string());

    // Filter reuse status
    tree.begin_child("Filter Reuse Status".to_string());

    let ipv4_status = if update_info.vs_ipv4_matcher_reused {
        "Reused (not recompiled)"
    } else {
        "Recompiled"
    };
    tree.add_empty_child(format!("IPv4 VS Matcher: {}", ipv4_status));

    let ipv6_status = if update_info.vs_ipv6_matcher_reused {
        "Reused (not recompiled)"
    } else {
        "Recompiled"
    };
    tree.add_empty_child(format!("IPv6 VS Matcher: {}", ipv6_status));

    tree.end_child();

    // ACL reuse information (only for update operations)
    if matches!(operation, VsOperation::Update) {
        if !update_info.vs_acl_reuses.is_empty() {
            tree.begin_child(format!(
                "ACL Filters Reused ({} virtual services)",
                update_info.vs_acl_reuses.len()
            ));
            for vs_id in &update_info.vs_acl_reuses {
                if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                    tree.add_empty_child(format_vs(ip, vs_id.port, vs_id.proto));
                }
            }
            tree.end_child();
        } else {
            tree.add_empty_child("ACL Filters Reused: None".to_string());
        }
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_vs_update_info_table(
    update_info: &balancerpb::UpdateInfo,
    operation: VsOperation,
) -> Result<(), Box<dyn Error>> {
    println!();
    println!("{}", "═".repeat(60).cyan().bold());
    let title = match operation {
        VsOperation::Update => "  VS Update Summary",
        VsOperation::Delete => "  VS Delete Result",
    };
    println!("{}", title.white().bold());
    println!("{}", "═".repeat(60).cyan().bold());
    println!();

    // Filter reuse status
    println!("{}", "Filter Reuse Status:".bright_cyan().bold());

    let ipv4_status = if update_info.vs_ipv4_matcher_reused {
        "✓ Reused (not recompiled)".bright_green()
    } else {
        "✗ Recompiled".bright_yellow()
    };
    println!("  IPv4 VS Matcher: {}", ipv4_status);

    let ipv6_status = if update_info.vs_ipv6_matcher_reused {
        "✓ Reused (not recompiled)".bright_green()
    } else {
        "✗ Recompiled".bright_yellow()
    };
    println!("  IPv6 VS Matcher: {}", ipv6_status);

    println!();

    // ACL reuse information (only for update operations)
    if matches!(operation, VsOperation::Update) {
        if !update_info.vs_acl_reuses.is_empty() {
            println!(
                "{} {}",
                "ACL Filters Reused:".bright_cyan().bold(),
                format!("({} virtual services)", update_info.vs_acl_reuses.len()).bright_white()
            );

            for vs_id in &update_info.vs_acl_reuses {
                if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                    println!("  • {}", format_vs(ip, vs_id.port, vs_id.proto).bright_yellow());
                }
            }
        } else {
            println!(
                "{} {}",
                "ACL Filters Reused:".bright_cyan().bold(),
                "None".bright_yellow()
            );
        }

        println!();
    }

    println!("{}", "═".repeat(60).cyan().bold());
    println!();

    Ok(())
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
    println!("{}", serde_json::to_string(&json)?);
    Ok(())
}

fn print_show_config_tree(response: &balancerpb::ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Balancer Configuration".to_string());

    tree.begin_child(format!("Config: {}", response.name));

    if let Some(config) = &response.config {
        if let Some(packet_handler) = &config.packet_handler {
            tree.begin_child("Packet Handler".to_string());

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
                        tree.begin_child(format!("[{}]", idx).cyan().to_string());
                        tree.add_empty_child(format!("VS: {}", format_vs(ip, vs_id.port, vs_id.proto)));
                        tree.add_empty_child(format!("Scheduler: {}", scheduler_to_string(vs.scheduler)));
                        tree.add_empty_child(format!("Flags: {}", format_flags(vs.flags.as_ref())));

                        if !vs.allowed_srcs.is_empty() {
                            tree.begin_child("Allowed Sources".to_string());
                            for subnet in &vs.allowed_srcs {
                                if let Ok(formatted) = crate::entities::format_allowed_src(subnet) {
                                    tree.add_empty_child(format!("- {}", formatted));
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

                        tree.begin_child(format!("Reals ({})", vs.reals.len()));
                        for (ridx, real) in vs.reals.iter().enumerate() {
                            if let Some(real_id) = &real.id {
                                if let Ok(dst) = opt_addr_to_ip(&real_id.ip) {
                                    tree.begin_child(format!("[{}]", ridx).cyan().to_string());
                                    tree.add_empty_child(format!("Real: {}", format_real(dst, real_id.port as u16)));
                                    tree.add_empty_child(format!("Weight: {}", real.weight));
                                    tree.end_child();
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
            tree.begin_child("State".to_string());
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
                tree.begin_child("WLC".to_string());
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
        for (idx, update) in response.buffered_real_updates.iter().enumerate() {
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
                    tree.begin_child(format!("[{}]", idx).cyan().to_string());
                    tree.add_empty_child(format!("Action: {}", action));
                    tree.add_empty_child(format!(
                        "VS: {}",
                        format_vs(
                            vip.parse()
                                .unwrap_or(std::net::IpAddr::V4(std::net::Ipv4Addr::UNSPECIFIED)),
                            vs_id.port,
                            vs_id.proto
                        )
                    ));
                    tree.add_empty_child(format!(
                        "Real: {}",
                        format_real(
                            rip.parse()
                                .unwrap_or(std::net::IpAddr::V4(std::net::Ipv4Addr::UNSPECIFIED)),
                            rel_real.port as u16
                        )
                    ));
                    tree.end_child();
                }
            }
        }
        tree.end_child();
    }

    tree.end_child();

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_show_config_table(response: &balancerpb::ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    let subtitle = Some(format!("Config: {}", response.name));
    print_boxed_header("BALANCER CONFIGURATION", subtitle.as_deref());
    println!();

    if let Some(config) = &response.config {
        if let Some(packet_handler) = &config.packet_handler {
            // Decap addresses (one per line, green color for list items)
            println!("{}", "Decap Addresses:".bright_cyan().bold());
            if !packet_handler.decap_addresses.is_empty() {
                for addr in &packet_handler.decap_addresses {
                    if let Ok(ip) = addr_to_ip(addr) {
                        println!("  {}", ip.to_string().bright_green());
                    }
                }
            } else {
                println!("  {}", "None".bright_green());
            }
            println!();

            // Source addresses
            println!("{}", "Source Addresses:".bright_cyan().bold());
            if let Ok(ipv4) = opt_addr_to_ip(&packet_handler.source_address_v4) {
                println!("  IPv4: {}", ipv4.to_string().bright_green());
            }
            if let Ok(ipv6) = opt_addr_to_ip(&packet_handler.source_address_v6) {
                println!("  IPv6: {}", ipv6.to_string().bright_green());
            }
            println!();

            // Session timeouts (one per line)
            if let Some(timeouts) = &packet_handler.sessions_timeouts {
                println!("{}", "Session Timeouts:".bright_cyan().bold());
                println!("  TCP: {}", format!("{}s", timeouts.tcp).bright_green());
                println!("  TCP SYN: {}", format!("{}s", timeouts.tcp_syn).bright_green());
                println!("  TCP SYN-ACK: {}", format!("{}s", timeouts.tcp_syn_ack).bright_green());
                println!("  TCP FIN: {}", format!("{}s", timeouts.tcp_fin).bright_green());
                println!("  UDP: {}", format!("{}s", timeouts.udp).bright_green());
                println!("  Default: {}", format!("{}s", timeouts.default).bright_green());
                println!();
            }
        }

        // State (one value per line)
        if let Some(state_config) = &config.state {
            println!("{}", "State:".bright_cyan().bold());

            if let Some(capacity) = state_config.session_table_capacity {
                println!("  Session Table Capacity: {}", format_number(capacity).bright_green());
            }

            if let Some(period) = &state_config.refresh_period {
                let refresh_period_ms = period.seconds * 1000 + period.nanos as i64 / 1_000_000;
                println!(
                    "  Refresh Period: {}",
                    format!("{}ms", refresh_period_ms).bright_green()
                );
            }

            if let Some(load_factor) = state_config.session_table_max_load_factor {
                println!("  Max Load Factor: {}", format!("{:.2}", load_factor).bright_green());
            }

            if let Some(wlc) = &state_config.wlc {
                if let Some(power) = wlc.power {
                    println!("  WLC Power: {}", power.to_string().bright_green());
                }
                if let Some(max_weight) = wlc.max_weight {
                    println!("  WLC Max Weight: {}", max_weight.to_string().bright_green());
                }
            }
            println!();
        }
    }

    // Virtual services (hierarchical display similar to info/stats)
    if let Some(config) = &response.config {
        if let Some(packet_handler) = &config.packet_handler {
            if !packet_handler.vs.is_empty() {
                // Print details for each VS
                for vs in &packet_handler.vs {
                    if let Some(vs_id) = &vs.id {
                        if let Ok(vs_ip) = opt_addr_to_ip(&vs_id.addr) {
                            println!(
                                "{}:",
                                format!("VS {}", format_vs(vs_ip, vs_id.port, vs_id.proto))
                                    .bright_yellow()
                                    .bold()
                            );

                            // Display VS properties on separate lines
                            println!("  Scheduler: {}", scheduler_to_string(vs.scheduler).bright_green());
                            println!("  Flags: {}", format_flags(vs.flags.as_ref()).bright_green());

                            // Peers - one per line with dash prefix
                            if !vs.peers.is_empty() {
                                println!("  Peers:");
                                for peer in &vs.peers {
                                    if let Ok(ip) = addr_to_ip(peer) {
                                        println!("    - {}", ip.to_string().bright_green());
                                    }
                                }
                            } else {
                                println!("  Peers: {}", "none".bright_green());
                            }

                            // Allowed sources - one per line with dash prefix
                            if !vs.allowed_srcs.is_empty() {
                                println!("  Allowed Sources:");
                                for src in &vs.allowed_srcs {
                                    if let Ok(formatted) = crate::entities::format_allowed_src(src) {
                                        println!("    - {}", formatted.bright_green());
                                    }
                                }
                            } else {
                                println!("  Allowed Sources: {}", "none".bright_green());
                            }

                            // Reals table
                            if !vs.reals.is_empty() {
                                #[derive(Tabled)]
                                struct RealRow {
                                    #[tabled(rename = "Real")]
                                    real: String,
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
                                            real: opt_addr_to_ip(&real_id.ip)
                                                .map(|ip| format_real(ip, real_id.port as u16))
                                                .unwrap_or_default(),
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
            println!("{}", serde_json::to_string(&json)?);
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
            println!("{}", serde_json::to_string(&json)?);
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
            for (vs_idx, vs_info) in info.vs.iter().enumerate() {
                if let Some(vs_id) = &vs_info.id {
                    if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                        tree.begin_child(format!("[{}]", vs_idx).cyan().to_string());
                        tree.add_empty_child(format!("VS: {}", format_vs(ip, vs_id.port, vs_id.proto)));
                        tree.add_empty_child(format!("Active Sessions: {}", format_number(vs_info.active_sessions)));
                        tree.add_empty_child(format!(
                            "Last Packet: {}",
                            format_timestamp(vs_info.last_packet_timestamp.as_ref())
                        ));

                        // Reals under this VS
                        if !vs_info.reals.is_empty() {
                            tree.begin_child(format!("Reals ({})", vs_info.reals.len()));
                            for (real_idx, real_info) in vs_info.reals.iter().enumerate() {
                                if let Some(real_id) = &real_info.id {
                                    if let Some(rel_real) = &real_id.real {
                                        if let Ok(real_ip) = opt_addr_to_ip(&rel_real.ip) {
                                            tree.begin_child(format!("[{}]", real_idx).cyan().to_string());
                                            tree.add_empty_child(format!(
                                                "Real: {}",
                                                format_real(real_ip, rel_real.port as u16)
                                            ));
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
    let subtitle = Some(format!("Config: {}", response.name));
    print_boxed_header("BALANCER INFO", subtitle.as_deref());

    println!();

    if let Some(info) = &response.info {
        println!(
            "Active Sessions: {}",
            format_number(info.active_sessions).bright_green()
        );
        println!(
            "Last Packet: {}",
            format_timestamp(info.last_packet_timestamp.as_ref()).bright_green()
        );
    }

    println!();

    if let Some(info) = &response.info {
        // VS table (hierarchical display - reals nested under VS)
        if !info.vs.is_empty() {
            for vs_info in &info.vs {
                if let Some(vs_id) = &vs_info.id {
                    if let Ok(vs_ip) = opt_addr_to_ip(&vs_id.addr) {
                        println!(
                            "{}:",
                            format!("VS {}", format_vs(vs_ip, vs_id.port, vs_id.proto))
                                .bright_yellow()
                                .bold()
                        );
                        println!(
                            "  Active Sessions: {}",
                            format_number(vs_info.active_sessions).bright_green()
                        );
                        println!(
                            "  Last Packet: {}",
                            format_timestamp(vs_info.last_packet_timestamp.as_ref()).bright_green()
                        );

                        if !vs_info.reals.is_empty() {
                            #[derive(Tabled)]
                            struct RealInfoRow {
                                #[tabled(rename = "Real")]
                                real: String,
                                #[tabled(rename = "Active Sessions")]
                                sessions: String,
                                #[tabled(rename = "Last Packet")]
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
            println!("{}", serde_json::to_string(&json)?);
        }
        OutputFormat::Tree => print_show_stats_tree(response)?,
        OutputFormat::Table => print_show_stats_table(response)?,
    }
    Ok(())
}

fn print_show_stats_tree(response: &balancerpb::ShowStatsResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Balancer Statistics".to_string());

    for (entry_idx, entry) in response.entries.iter().enumerate() {
        tree.begin_child(format!("[{}]", entry_idx).cyan().to_string());
        tree.add_empty_child(format!("Config: {}", entry.name));

        if let Some(ref_info) = &entry.r#ref {
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
        }

        if let Some(stats) = &entry.stats {
            // Module stats (split into components)
            tree.begin_child("Module".to_string());

            if let Some(common) = &stats.common {
                tree.begin_child("Common".to_string());
                tree.add_empty_child(format!("Incoming Packets: {}", format_number(common.incoming_packets)));
                tree.add_empty_child(format!("Incoming Bytes: {}", format_bytes(common.incoming_bytes)));
                tree.add_empty_child(format!(
                    "Unexpected Network Proto: {}",
                    format_number(common.unexpected_network_proto)
                ));
                tree.add_empty_child(format!("Decap Successful: {}", format_number(common.decap_successful)));
                tree.add_empty_child(format!("Decap Failed: {}", format_number(common.decap_failed)));
                tree.add_empty_child(format!("Outgoing Packets: {}", format_number(common.outgoing_packets)));
                tree.add_empty_child(format!("Outgoing Bytes: {}", format_bytes(common.outgoing_bytes)));
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
                tree.add_empty_child(format!(
                    "Payload Too Short IP: {}",
                    format_number(icmpv4.payload_too_short_ip)
                ));
                tree.add_empty_child(format!(
                    "Unmatching Src From Original: {}",
                    format_number(icmpv4.unmatching_src_from_original)
                ));
                tree.add_empty_child(format!(
                    "Payload Too Short Port: {}",
                    format_number(icmpv4.payload_too_short_port)
                ));
                tree.add_empty_child(format!(
                    "Unexpected Transport: {}",
                    format_number(icmpv4.unexpected_transport)
                ));
                tree.add_empty_child(format!("Unrecognized VS: {}", format_number(icmpv4.unrecognized_vs)));
                tree.add_empty_child(format!(
                    "Forwarded Packets: {}",
                    format_number(icmpv4.forwarded_packets)
                ));
                tree.add_empty_child(format!(
                    "Broadcasted Packets: {}",
                    format_number(icmpv4.broadcasted_packets)
                ));
                tree.add_empty_child(format!(
                    "Packet Clones Sent: {}",
                    format_number(icmpv4.packet_clones_sent)
                ));
                tree.add_empty_child(format!(
                    "Packet Clones Received: {}",
                    format_number(icmpv4.packet_clones_received)
                ));
                tree.add_empty_child(format!(
                    "Packet Clone Failures: {}",
                    format_number(icmpv4.packet_clone_failures)
                ));
                tree.end_child();
            }

            if let Some(icmpv6) = &stats.icmpv6 {
                tree.begin_child("ICMPv6".to_string());
                tree.add_empty_child(format!("Incoming Packets: {}", format_number(icmpv6.incoming_packets)));
                tree.add_empty_child(format!("Src Not Allowed: {}", format_number(icmpv6.src_not_allowed)));
                tree.add_empty_child(format!("Echo Responses: {}", format_number(icmpv6.echo_responses)));
                tree.add_empty_child(format!(
                    "Payload Too Short IP: {}",
                    format_number(icmpv6.payload_too_short_ip)
                ));
                tree.add_empty_child(format!(
                    "Unmatching Src From Original: {}",
                    format_number(icmpv6.unmatching_src_from_original)
                ));
                tree.add_empty_child(format!(
                    "Payload Too Short Port: {}",
                    format_number(icmpv6.payload_too_short_port)
                ));
                tree.add_empty_child(format!(
                    "Unexpected Transport: {}",
                    format_number(icmpv6.unexpected_transport)
                ));
                tree.add_empty_child(format!("Unrecognized VS: {}", format_number(icmpv6.unrecognized_vs)));
                tree.add_empty_child(format!(
                    "Forwarded Packets: {}",
                    format_number(icmpv6.forwarded_packets)
                ));
                tree.add_empty_child(format!(
                    "Broadcasted Packets: {}",
                    format_number(icmpv6.broadcasted_packets)
                ));
                tree.add_empty_child(format!(
                    "Packet Clones Sent: {}",
                    format_number(icmpv6.packet_clones_sent)
                ));
                tree.add_empty_child(format!(
                    "Packet Clones Received: {}",
                    format_number(icmpv6.packet_clones_received)
                ));
                tree.add_empty_child(format!(
                    "Packet Clone Failures: {}",
                    format_number(icmpv6.packet_clone_failures)
                ));
                tree.end_child();
            }

            tree.end_child(); // Module

            // VS stats
            if !stats.vs.is_empty() {
                tree.begin_child(format!("Virtual Services ({})", stats.vs.len()));
                for (vs_idx, vs) in stats.vs.iter().enumerate() {
                    if let Some(vs_id) = &vs.vs {
                        if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                            tree.begin_child(format!("[{}]", vs_idx).cyan().to_string());
                            tree.add_empty_child(format!("VS: {}", format_vs(ip, vs_id.port, vs_id.proto)));
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
                                tree.add_empty_child(format!(
                                    "Created Sessions: {}",
                                    format_number(s.created_sessions)
                                ));
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
                                tree.add_empty_child(format!(
                                    "Echo ICMP Packets: {}",
                                    format_number(s.echo_icmp_packets)
                                ));
                                tree.add_empty_child(format!(
                                    "Error ICMP Packets: {}",
                                    format_number(s.error_icmp_packets)
                                ));
                                tree.add_empty_child(format!(
                                    "Real Is Disabled: {}",
                                    format_number(s.real_is_disabled)
                                ));
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
                                tree.begin_child(format!("Reals ({})", vs.reals.len()));
                                for (real_idx, real) in vs.reals.iter().enumerate() {
                                    if let Some(real_id) = &real.real {
                                        if let Some(rel_real) = &real_id.real {
                                            if let Ok(real_ip) = opt_addr_to_ip(&rel_real.ip) {
                                                tree.begin_child(format!("[{}]", real_idx).cyan().to_string());
                                                tree.add_empty_child(format!(
                                                    "Real: {}",
                                                    format_real(real_ip, rel_real.port as u16)
                                                ));
                                                if let Some(s) = &real.stats {
                                                    tree.add_empty_child(format!(
                                                        "Packets: {}",
                                                        format_number(s.packets)
                                                    ));
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

                            // Allowed sources stats
                            if !vs.allowed_sources.is_empty() {
                                tree.begin_child("Allowed Sources".to_string());
                                for allowed_src in &vs.allowed_sources {
                                    let tag_str = if allowed_src.tag.is_empty() {
                                        "None".to_string()
                                    } else {
                                        allowed_src.tag.clone()
                                    };
                                    tree.add_empty_child(format!(
                                        "Tag {}: {} passes",
                                        tag_str,
                                        format_number(allowed_src.passes)
                                    ));
                                }
                                tree.end_child();
                            }

                            tree.end_child(); // VS
                        }
                    }
                }
                tree.end_child();
            }
        }

        tree.end_child(); // entry
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_show_stats_table(response: &balancerpb::ShowStatsResponse) -> Result<(), Box<dyn Error>> {
    if response.entries.is_empty() {
        print_boxed_header("BALANCER STATISTICS", Some("Entries: 0"));
        println!();
        return Ok(());
    }

    for (idx, entry) in response.entries.iter().enumerate() {
        if idx > 0 {
            println!("{}", "─".repeat(80).bright_black());
            println!();
        }

        // Print header with topology info and config name as two-line subtitle
        let subtitle = if let Some(ref_info) = &entry.r#ref {
            format!(
                "Config: {} | Device: {}\nPipeline: {} | Function: {} | Chain: {}",
                entry.name,
                ref_info.device.as_deref().unwrap_or("N/A"),
                ref_info.pipeline.as_deref().unwrap_or("N/A"),
                ref_info.function.as_deref().unwrap_or("N/A"),
                ref_info.chain.as_deref().unwrap_or("N/A"),
            )
        } else {
            format!("Config: {}", entry.name)
        };
        print_boxed_header("BALANCER STATISTICS", Some(&subtitle));
        println!();

        let Some(stats) = &entry.stats else {
            continue;
        };

        println!("{}", "Module:".bright_yellow().bold());

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
                metric: "Unexpected Proto".to_string(),
                value: format_number(common.unexpected_network_proto),
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
        }

        // Separator between Common and L4
        if stats.common.is_some() && stats.l4.is_some() {
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "".to_string(),
                value: "".to_string(),
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

        // Separator between L4 and ICMPv4
        if stats.l4.is_some() && stats.icmpv4.is_some() {
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "".to_string(),
                value: "".to_string(),
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
                metric: "Payload Short IP".to_string(),
                value: format_number(icmpv4.payload_too_short_ip),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Unmatch Src Orig".to_string(),
                value: format_number(icmpv4.unmatching_src_from_original),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Payload Short Port".to_string(),
                value: format_number(icmpv4.payload_too_short_port),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Unexpected Trans".to_string(),
                value: format_number(icmpv4.unexpected_transport),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Unrecognized VS".to_string(),
                value: format_number(icmpv4.unrecognized_vs),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Forwarded Pkts".to_string(),
                value: format_number(icmpv4.forwarded_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Broadcasted Pkts".to_string(),
                value: format_number(icmpv4.broadcasted_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Clones Sent".to_string(),
                value: format_number(icmpv4.packet_clones_sent),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Clones Received".to_string(),
                value: format_number(icmpv4.packet_clones_received),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Clone Failures".to_string(),
                value: format_number(icmpv4.packet_clone_failures),
            });
        }

        // Separator between ICMPv4 and ICMPv6
        if stats.icmpv4.is_some() && stats.icmpv6.is_some() {
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "".to_string(),
                value: "".to_string(),
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
                metric: "Payload Short IP".to_string(),
                value: format_number(icmpv6.payload_too_short_ip),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Unmatch Src Orig".to_string(),
                value: format_number(icmpv6.unmatching_src_from_original),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Payload Short Port".to_string(),
                value: format_number(icmpv6.payload_too_short_port),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Unexpected Trans".to_string(),
                value: format_number(icmpv6.unexpected_transport),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Unrecognized VS".to_string(),
                value: format_number(icmpv6.unrecognized_vs),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Forwarded Pkts".to_string(),
                value: format_number(icmpv6.forwarded_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Broadcasted Pkts".to_string(),
                value: format_number(icmpv6.broadcasted_packets),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Clones Sent".to_string(),
                value: format_number(icmpv6.packet_clones_sent),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Clones Received".to_string(),
                value: format_number(icmpv6.packet_clones_received),
            });
            rows.push(ModuleStatsRow {
                category: "".to_string(),
                metric: "Clone Failures".to_string(),
                value: format_number(icmpv6.packet_clone_failures),
            });
        }

        let table = Table::new(rows).with(Style::rounded()).to_string();
        println!("{}", table);
        println!();

        // VS Stats (hierarchical display - reals nested under VS, similar to info)
        if !stats.vs.is_empty() {
            for vs in &stats.vs {
                if let Some(vs_id) = &vs.vs {
                    if let Ok(vs_ip) = opt_addr_to_ip(&vs_id.addr) {
                        println!(
                            "{}:",
                            format!("VS {}", format_vs(vs_ip, vs_id.port, vs_id.proto))
                                .bright_yellow()
                                .bold()
                        );

                        // Display VS-level stats - one metric per line
                        if let Some(s) = &vs.stats {
                            println!(
                                "  Incoming Packets: {}",
                                format_number(s.incoming_packets).bright_green()
                            );
                            println!("  Incoming Bytes: {}", format_bytes(s.incoming_bytes).bright_green());
                            println!(
                                "  Outgoing Packets: {}",
                                format_number(s.outgoing_packets).bright_green()
                            );
                            println!("  Outgoing Bytes: {}", format_bytes(s.outgoing_bytes).bright_green());
                            println!(
                                "  Created Sessions: {}",
                                format_number(s.created_sessions).bright_green()
                            );
                            println!("  OPS Packets: {}", format_number(s.ops_packets).bright_green());
                            println!(
                                "  Packet Src Not Allowed: {}",
                                format_number(s.packet_src_not_allowed).bright_green()
                            );
                            println!(
                                "  Session Table Overflow: {}",
                                format_number(s.session_table_overflow).bright_green()
                            );
                            println!(
                                "  Not Rescheduled Packets: {}",
                                format_number(s.not_rescheduled_packets).bright_green()
                            );
                            println!(
                                "  Real Is Disabled: {}",
                                format_number(s.real_is_disabled).bright_green()
                            );
                            println!("  Real Is Removed: {}", format_number(s.real_is_removed).bright_green());
                            println!("  No Reals: {}", format_number(s.no_reals).bright_green());
                            println!(
                                "  Echo ICMP Packets: {}",
                                format_number(s.echo_icmp_packets).bright_green()
                            );
                            println!(
                                "  Error ICMP Packets: {}",
                                format_number(s.error_icmp_packets).bright_green()
                            );
                            println!(
                                "  Broadcasted ICMP Packets: {}",
                                format_number(s.broadcasted_icmp_packets).bright_green()
                            );
                        }

                        // Display allowed sources stats
                        if !vs.allowed_sources.is_empty() {
                            println!("  {}:", "Allowed Sources".bright_cyan().bold());
                            for allowed_src in &vs.allowed_sources {
                                let tag_str = if allowed_src.tag.is_empty() {
                                    "None".to_string()
                                } else {
                                    allowed_src.tag.clone()
                                };
                                println!(
                                    "    Tag {}: {}",
                                    tag_str,
                                    format_number(allowed_src.passes).bright_green()
                                );
                            }
                        } else {
                            println!(
                                "  {}: {}",
                                "Allowed Sources".bright_cyan().bold(),
                                "None".bright_green()
                            );
                        }

                        // Display reals table for this VS
                        if !vs.reals.is_empty() {
                            #[derive(Tabled)]
                            struct RealStatsRow {
                                #[tabled(rename = "Real")]
                                real: String,
                                #[tabled(rename = "Packets")]
                                packets: String,
                                #[tabled(rename = "Bytes")]
                                bytes: String,
                                #[tabled(rename = "Created Sessions")]
                                sessions: String,
                                #[tabled(rename = "Disabled Pkts")]
                                disabled: String,
                                #[tabled(rename = "OPS Pkts")]
                                ops: String,
                                #[tabled(rename = "ICMP Pkts")]
                                error_icmp: String,
                            }

                            let real_rows: Vec<RealStatsRow> = vs
                                .reals
                                .iter()
                                .filter_map(|real| {
                                    real.real.as_ref().and_then(|real_id| {
                                        real_id.real.as_ref().and_then(|rel_real| {
                                            opt_addr_to_ip(&rel_real.ip).ok().map(|real_ip| {
                                                let s = real.stats.as_ref();
                                                RealStatsRow {
                                                    real: format_real(real_ip, rel_real.port as u16),
                                                    packets: s
                                                        .map(|s| format_number(s.packets))
                                                        .unwrap_or_else(|| "0".to_string()),
                                                    bytes: s
                                                        .map(|s| format_bytes(s.bytes))
                                                        .unwrap_or_else(|| "0 B".to_string()),
                                                    sessions: s
                                                        .map(|s| format_number(s.created_sessions))
                                                        .unwrap_or_else(|| "0".to_string()),
                                                    disabled: s
                                                        .map(|s| format_number(s.packets_real_disabled))
                                                        .unwrap_or_else(|| "0".to_string()),
                                                    ops: s
                                                        .map(|s| format_number(s.ops_packets))
                                                        .unwrap_or_else(|| "0".to_string()),
                                                    error_icmp: s
                                                        .map(|s| format_number(s.error_icmp_packets))
                                                        .unwrap_or_else(|| "0".to_string()),
                                                }
                                            })
                                        })
                                    })
                                })
                                .collect();

                            let table = Table::new(real_rows).with(Style::rounded()).to_string();
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
// ShowSessions Output
////////////////////////////////////////////////////////////////////////////////

pub fn print_show_sessions(
    response: &balancerpb::ShowSessionsResponse,
    format: OutputFormat,
) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => {
            let json = json_output::convert_show_sessions(response);
            println!("{}", serde_json::to_string(&json)?);
        }
        OutputFormat::Tree => print_show_sessions_tree(response)?,
        OutputFormat::Table => print_show_sessions_table(response)?,
    }
    Ok(())
}

fn print_show_sessions_tree(response: &balancerpb::ShowSessionsResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Active Sessions".to_string());

    tree.begin_child(format!("Config: {}", response.name));

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
                    tree.begin_child(format!("[{}]", idx).cyan().to_string());
                    tree.add_empty_child(format!(
                        "Client: {}",
                        format_ip_port(client, session.client_port as u16)
                    ));
                    tree.add_empty_child(format!("VS: {}", format_vs(vs_ip, vs_id.port, vs_id.proto)));
                    tree.add_empty_child(format!("Real: {}", format_real(real_ip, rel_real.port as u16)));
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

    tree.end_child();

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_show_sessions_table(response: &balancerpb::ShowSessionsResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    let subtitle = Some(format!("Config: {}", response.name));
    print_boxed_header("BALANCER SESSIONS", subtitle.as_deref());

    println!();
    println!(
        " Total Sessions: {}",
        format_number(response.sessions.len() as u64).bright_green()
    );

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
            #[tabled(rename = "Created At")]
            created_at: String,
            #[tabled(rename = "Last Packet")]
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
                                client: format_ip_port(client_ip, session.client_port as u16),
                                vs: format_ip_port(vs_ip, vs_id.port as u16),
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
            println!("{}", serde_json::to_string(&json)?);
        }
        OutputFormat::Tree => print_show_graph_tree(response)?,
        OutputFormat::Table => print_show_graph_table(response)?,
    }
    Ok(())
}

fn print_show_graph_tree(response: &balancerpb::ShowGraphResponse) -> Result<(), Box<dyn Error>> {
    let mut tree: TreeBuilder = TreeBuilder::new("Balancer Graph".to_string());

    tree.begin_child(format!("Config: {}", response.name));

    if let Some(graph) = &response.graph {
        if !graph.virtual_services.is_empty() {
            tree.begin_child(format!("Virtual Services ({})", graph.virtual_services.len()));

            for (vs_idx, vs) in graph.virtual_services.iter().enumerate() {
                if let Some(vs_id) = &vs.identifier {
                    if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                        tree.begin_child(format!("[{}]", vs_idx).cyan().to_string());
                        tree.add_empty_child(format!("VS: {}", format_vs(ip, vs_id.port, vs_id.proto)));

                        if !vs.reals.is_empty() {
                            tree.begin_child(format!("Reals ({})", vs.reals.len()));
                            for (real_idx, real) in vs.reals.iter().enumerate() {
                                if let Some(real_id) = &real.identifier {
                                    if let Ok(real_ip) = opt_addr_to_ip(&real_id.ip) {
                                        let status = if real.enabled { "enabled" } else { "disabled" };
                                        tree.begin_child(format!("[{}]", real_idx).cyan().to_string());
                                        tree.add_empty_child(format!(
                                            "Real: {}",
                                            format_real(real_ip, real_id.port as u16)
                                        ));
                                        tree.add_empty_child(format!("Weight: {}", real.weight));
                                        tree.add_empty_child(format!("Effective Weight: {}", real.effective_weight));
                                        tree.add_empty_child(format!("Status: {}", status));
                                        tree.end_child();
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

fn print_show_graph_table(response: &balancerpb::ShowGraphResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    let subtitle = Some(format!("Config: {}", response.name));
    print_boxed_header("BALANCER GRAPH", subtitle.as_deref());

    println!();

    if let Some(graph) = &response.graph {
        // Display each VS with its reals in a hierarchical format (similar to info
        // output)
        if !graph.virtual_services.is_empty() {
            for vs in &graph.virtual_services {
                if let Some(vs_id) = &vs.identifier {
                    if let Ok(vs_ip) = opt_addr_to_ip(&vs_id.addr) {
                        println!(
                            "{}:",
                            format!("VS {}", format_vs(vs_ip, vs_id.port, vs_id.proto))
                                .bright_yellow()
                                .bold()
                        );

                        if !vs.reals.is_empty() {
                            #[derive(Tabled)]
                            struct RealGraphRow {
                                #[tabled(rename = "Real")]
                                real: String,
                                #[tabled(rename = "Weight")]
                                weight: String,
                                #[tabled(rename = "Effective Weight")]
                                effective_weight: String,
                                #[tabled(rename = "Status")]
                                status: String,
                            }

                            let rows: Vec<RealGraphRow> = vs
                                .reals
                                .iter()
                                .filter_map(|real| {
                                    real.identifier.as_ref().and_then(|real_id| {
                                        opt_addr_to_ip(&real_id.ip).ok().map(|real_ip| RealGraphRow {
                                            real: format_real(real_ip, real_id.port as u16),
                                            weight: real.weight.to_string(),
                                            effective_weight: real.effective_weight.to_string(),
                                            status: if real.enabled {
                                                "enabled".to_string()
                                            } else {
                                                "disabled".to_string()
                                            },
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
// ShowInspect Output
////////////////////////////////////////////////////////////////////////////////

pub fn print_show_inspect(
    response: &balancerpb::ShowInspectResponse,
    format: InspectOutputFormat,
) -> Result<(), Box<dyn Error>> {
    match format {
        InspectOutputFormat::Json => print_show_inspect_json(response),
        InspectOutputFormat::Normal => print_show_inspect_normal(response),
        InspectOutputFormat::Detail => print_show_inspect_detail(response),
    }
}

fn print_show_inspect_json(response: &balancerpb::ShowInspectResponse) -> Result<(), Box<dyn Error>> {
    let json = json_output::convert_show_inspect(response);
    println!("{}", serde_json::to_string(&json)?); // Compact, not pretty
    Ok(())
}

fn print_show_inspect_normal(response: &balancerpb::ShowInspectResponse) -> Result<(), Box<dyn Error>> {
    let inspect = match &response.inspect {
        Some(i) => i,
        None => return Ok(()),
    };

    // Print header
    print_boxed_header("BALANCER MEMORY INSPECTION", None);
    println!();

    // Agent-level memory
    let usage_percent = if inspect.memory_limit > 0 {
        (inspect.memory_usage as f64 / inspect.memory_limit as f64) * 100.0
    } else {
        0.0
    };

    println!("{}", "Agent Memory:".bright_cyan().bold());
    println!("  Limit: {}", format_bytes(inspect.memory_limit).bright_green());
    println!(
        "  Usage: {} ({:.1}%)",
        format_bytes(inspect.memory_usage).bright_green(),
        usage_percent
    );
    println!();

    // Per-balancer information
    for (balancer_idx, balancer) in inspect.balancers.iter().enumerate() {
        if balancer_idx > 0 {
            println!("{}", "─".repeat(80).bright_black());
            println!();
        }

        println!(
            "{} {}",
            format!("Balancer \"{}\":", balancer.name).bright_yellow().bold(),
            format_bytes(balancer.total_usage).bright_green()
        );

        // Packet handler breakdown
        if let Some(ph) = &balancer.packet_handler_inspect {
            let ph_percent = if balancer.total_usage > 0 {
                (ph.total_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!();
            println!(
                "  {} {} ({:.1}%)",
                "Packet Handler:".bright_cyan().bold(),
                format_bytes(ph.total_usage).bright_green(),
                ph_percent
            );

            // IPv4 section
            if let Some(ipv4) = &ph.vs_ipv4_inspect {
                let ipv4_percent = if balancer.total_usage > 0 {
                    (ipv4.total_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!();
                println!(
                    "    {} {} ({:.1}%)",
                    "IPv4 VS Section:".bright_white(),
                    format_bytes(ipv4.total_usage).bright_green(),
                    ipv4_percent
                );
                let matcher_pct = if balancer.total_usage > 0 {
                    (ipv4.matcher_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Matcher: {} ({:.1}%)",
                    format_bytes(ipv4.matcher_usage).bright_green(),
                    matcher_pct
                );
                let announce_pct = if balancer.total_usage > 0 {
                    (ipv4.announce_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Announce: {} ({:.1}%)",
                    format_bytes(ipv4.announce_usage).bright_green(),
                    announce_pct
                );
                let index_pct = if balancer.total_usage > 0 {
                    (ipv4.index_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Index: {} ({:.1}%)",
                    format_bytes(ipv4.index_usage).bright_green(),
                    index_pct
                );
            }

            // IPv6 section
            if let Some(ipv6) = &ph.vs_ipv6_inspect {
                let ipv6_percent = if balancer.total_usage > 0 {
                    (ipv6.total_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!();
                println!(
                    "    {} {} ({:.1}%)",
                    "IPv6 VS Section:".bright_white(),
                    format_bytes(ipv6.total_usage).bright_green(),
                    ipv6_percent
                );
                let matcher_pct = if balancer.total_usage > 0 {
                    (ipv6.matcher_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Matcher: {} ({:.1}%)",
                    format_bytes(ipv6.matcher_usage).bright_green(),
                    matcher_pct
                );
                let announce_pct = if balancer.total_usage > 0 {
                    (ipv6.announce_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Announce: {} ({:.1}%)",
                    format_bytes(ipv6.announce_usage).bright_green(),
                    announce_pct
                );
                let index_pct = if balancer.total_usage > 0 {
                    (ipv6.index_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Index: {} ({:.1}%)",
                    format_bytes(ipv6.index_usage).bright_green(),
                    index_pct
                );
            }

            // Other packet handler components
            println!();
            let vs_index_pct = if balancer.total_usage > 0 {
                (ph.vs_index_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    VS Index: {} ({:.1}%)",
                format_bytes(ph.vs_index_usage).bright_green(),
                vs_index_pct
            );
            let reals_index_pct = if balancer.total_usage > 0 {
                (ph.reals_index_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    Reals Index: {} ({:.1}%)",
                format_bytes(ph.reals_index_usage).bright_green(),
                reals_index_pct
            );
            let counters_pct = if balancer.total_usage > 0 {
                (ph.counters_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    Counters: {} ({:.1}%)",
                format_bytes(ph.counters_usage).bright_green(),
                counters_pct
            );
            let decap_pct = if balancer.total_usage > 0 {
                (ph.decap_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    Decap: {} ({:.1}%)",
                format_bytes(ph.decap_usage).bright_green(),
                decap_pct
            );
        }

        // State breakdown
        if let Some(state) = &balancer.state_inspect {
            let state_percent = if balancer.total_usage > 0 {
                (state.total_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!();
            println!(
                "  {} {} ({:.1}%)",
                "State:".bright_cyan().bold(),
                format_bytes(state.total_usage).bright_green(),
                state_percent
            );
            let vs_reg_pct = if balancer.total_usage > 0 {
                (state.vs_registry_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    VS Registry: {} ({:.1}%)",
                format_bytes(state.vs_registry_usage).bright_green(),
                vs_reg_pct
            );
            let reals_reg_pct = if balancer.total_usage > 0 {
                (state.reals_registry_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    Reals Registry: {} ({:.1}%)",
                format_bytes(state.reals_registry_usage).bright_green(),
                reals_reg_pct
            );
            let session_pct = if balancer.total_usage > 0 {
                (state.session_table_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    Session Table: {} ({:.1}%)",
                format_bytes(state.session_table_usage).bright_green(),
                session_pct
            );
        }

        // Other usage
        let other_percent = if balancer.total_usage > 0 {
            (balancer.other_usage as f64 / balancer.total_usage as f64) * 100.0
        } else {
            0.0
        };
        println!();
        println!(
            "  {} {} ({:.1}%)",
            "Other:".bright_cyan().bold(),
            format_bytes(balancer.other_usage).cyan(),
            other_percent
        );
        println!();
    }

    Ok(())
}

fn print_show_inspect_detail(response: &balancerpb::ShowInspectResponse) -> Result<(), Box<dyn Error>> {
    let inspect = match &response.inspect {
        Some(i) => i,
        None => return Ok(()),
    };

    // Print header
    print_boxed_header("BALANCER MEMORY INSPECTION (DETAILED)", None);
    println!();

    // Agent-level memory
    let usage_percent = if inspect.memory_limit > 0 {
        (inspect.memory_usage as f64 / inspect.memory_limit as f64) * 100.0
    } else {
        0.0
    };

    println!("{}", "Agent Memory:".bright_cyan().bold());
    println!("  Limit: {}", format_bytes(inspect.memory_limit).bright_green());
    println!(
        "  Usage: {} ({:.1}%)",
        format_bytes(inspect.memory_usage).bright_green(),
        usage_percent
    );
    println!();

    // Per-balancer information
    for (balancer_idx, balancer) in inspect.balancers.iter().enumerate() {
        if balancer_idx > 0 {
            println!("{}", "─".repeat(80).bright_black());
            println!();
        }

        println!(
            "{} {}",
            format!("Balancer \"{}\":", balancer.name).bright_yellow().bold(),
            format_bytes(balancer.total_usage).bright_green()
        );

        // Packet handler breakdown
        if let Some(ph) = &balancer.packet_handler_inspect {
            let ph_percent = if balancer.total_usage > 0 {
                (ph.total_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!();
            println!(
                "  {} {} ({:.1}%)",
                "Packet Handler:".bright_cyan().bold(),
                format_bytes(ph.total_usage).bright_green(),
                ph_percent
            );

            // IPv4 section with VS details
            if let Some(ipv4) = &ph.vs_ipv4_inspect {
                let ipv4_percent = if balancer.total_usage > 0 {
                    (ipv4.total_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!();
                println!(
                    "    {} {} ({:.1}%)",
                    "IPv4 VS Section:".bright_white(),
                    format_bytes(ipv4.total_usage).bright_green(),
                    ipv4_percent
                );
                let matcher_pct = if balancer.total_usage > 0 {
                    (ipv4.matcher_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Matcher: {} ({:.1}%)",
                    format_bytes(ipv4.matcher_usage).bright_green(),
                    matcher_pct
                );
                let announce_pct = if balancer.total_usage > 0 {
                    (ipv4.announce_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Announce: {} ({:.1}%)",
                    format_bytes(ipv4.announce_usage).bright_green(),
                    announce_pct
                );
                let index_pct = if balancer.total_usage > 0 {
                    (ipv4.index_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Index: {} ({:.1}%)",
                    format_bytes(ipv4.index_usage).bright_green(),
                    index_pct
                );

                // Per-VS breakdown
                if !ipv4.vs_inspects.is_empty() {
                    println!();
                    println!(
                        "      {} ({}):",
                        "Virtual Services".bright_white(),
                        ipv4.vs_inspects.len()
                    );
                    for (idx, vs_inspect) in ipv4.vs_inspects.iter().enumerate() {
                        if let Some(vs_id) = &vs_inspect.identifier {
                            if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                                if let Some(inspect) = &vs_inspect.inspect {
                                    let vs_percent = if balancer.total_usage > 0 {
                                        (inspect.total_usage as f64 / balancer.total_usage as f64) * 100.0
                                    } else {
                                        0.0
                                    };
                                    println!(
                                        "        [{}] {} {} ({:.1}%)",
                                        idx,
                                        format_vs(ip, vs_id.port, vs_id.proto),
                                        format_bytes(inspect.total_usage).bright_green(),
                                        vs_percent
                                    );
                                    let acl_pct = if balancer.total_usage > 0 {
                                        (inspect.acl_usage as f64 / balancer.total_usage as f64) * 100.0
                                    } else {
                                        0.0
                                    };
                                    println!(
                                        "          ACL: {} ({:.1}%)",
                                        format_bytes(inspect.acl_usage).bright_green(),
                                        acl_pct
                                    );
                                    let ring_pct = if balancer.total_usage > 0 {
                                        (inspect.ring_usage as f64 / balancer.total_usage as f64) * 100.0
                                    } else {
                                        0.0
                                    };
                                    println!(
                                        "          Ring: {} ({:.1}%)",
                                        format_bytes(inspect.ring_usage).bright_green(),
                                        ring_pct
                                    );
                                    let counters_pct = if balancer.total_usage > 0 {
                                        (inspect.counters_usage as f64 / balancer.total_usage as f64) * 100.0
                                    } else {
                                        0.0
                                    };
                                    println!(
                                        "          Counters: {} ({:.1}%)",
                                        format_bytes(inspect.counters_usage).bright_green(),
                                        counters_pct
                                    );
                                    if let Some(reals) = &inspect.reals_usage {
                                        let reals_pct = if balancer.total_usage > 0 {
                                            (reals.total_usage as f64 / balancer.total_usage as f64) * 100.0
                                        } else {
                                            0.0
                                        };
                                        println!(
                                            "          Reals: {} ({:.1}%)",
                                            format_bytes(reals.total_usage).bright_green(),
                                            reals_pct
                                        );
                                        let reals_counters_pct = if balancer.total_usage > 0 {
                                            (reals.counters_usage as f64 / balancer.total_usage as f64) * 100.0
                                        } else {
                                            0.0
                                        };
                                        println!(
                                            "            Counters: {} ({:.1}%)",
                                            format_bytes(reals.counters_usage).bright_green(),
                                            reals_counters_pct
                                        );
                                        let reals_data_pct = if balancer.total_usage > 0 {
                                            (reals.data_usage as f64 / balancer.total_usage as f64) * 100.0
                                        } else {
                                            0.0
                                        };
                                        println!(
                                            "            Data: {} ({:.1}%)",
                                            format_bytes(reals.data_usage).bright_green(),
                                            reals_data_pct
                                        );
                                    }
                                    let other_pct = if balancer.total_usage > 0 {
                                        (inspect.other_usage as f64 / balancer.total_usage as f64) * 100.0
                                    } else {
                                        0.0
                                    };
                                    println!(
                                        "          Other: {} ({:.1}%)",
                                        format_bytes(inspect.other_usage).bright_green(),
                                        other_pct
                                    );
                                }
                            }
                        }
                    }
                }
            }

            // IPv6 section with VS details
            if let Some(ipv6) = &ph.vs_ipv6_inspect {
                let ipv6_percent = if balancer.total_usage > 0 {
                    (ipv6.total_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!();
                println!(
                    "    {} {} ({:.1}%)",
                    "IPv6 VS Section:".bright_white(),
                    format_bytes(ipv6.total_usage).bright_green(),
                    ipv6_percent
                );
                let matcher_pct = if balancer.total_usage > 0 {
                    (ipv6.matcher_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Matcher: {} ({:.1}%)",
                    format_bytes(ipv6.matcher_usage).bright_green(),
                    matcher_pct
                );
                let announce_pct = if balancer.total_usage > 0 {
                    (ipv6.announce_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Announce: {} ({:.1}%)",
                    format_bytes(ipv6.announce_usage).bright_green(),
                    announce_pct
                );
                let index_pct = if balancer.total_usage > 0 {
                    (ipv6.index_usage as f64 / balancer.total_usage as f64) * 100.0
                } else {
                    0.0
                };
                println!(
                    "      Index: {} ({:.1}%)",
                    format_bytes(ipv6.index_usage).bright_green(),
                    index_pct
                );

                // Per-VS breakdown
                if !ipv6.vs_inspects.is_empty() {
                    println!();
                    println!(
                        "      {} ({}):",
                        "Virtual Services".bright_white(),
                        ipv6.vs_inspects.len()
                    );
                    for (idx, vs_inspect) in ipv6.vs_inspects.iter().enumerate() {
                        if let Some(vs_id) = &vs_inspect.identifier {
                            if let Ok(ip) = opt_addr_to_ip(&vs_id.addr) {
                                if let Some(inspect) = &vs_inspect.inspect {
                                    let vs_percent = if balancer.total_usage > 0 {
                                        (inspect.total_usage as f64 / balancer.total_usage as f64) * 100.0
                                    } else {
                                        0.0
                                    };
                                    println!(
                                        "        [{}] {} {} ({:.1}%)",
                                        idx,
                                        format_vs(ip, vs_id.port, vs_id.proto),
                                        format_bytes(inspect.total_usage).bright_green(),
                                        vs_percent
                                    );
                                    let acl_pct = if balancer.total_usage > 0 {
                                        (inspect.acl_usage as f64 / balancer.total_usage as f64) * 100.0
                                    } else {
                                        0.0
                                    };
                                    println!(
                                        "          ACL: {} ({:.1}%)",
                                        format_bytes(inspect.acl_usage).bright_green(),
                                        acl_pct
                                    );
                                    let ring_pct = if balancer.total_usage > 0 {
                                        (inspect.ring_usage as f64 / balancer.total_usage as f64) * 100.0
                                    } else {
                                        0.0
                                    };
                                    println!(
                                        "          Ring: {} ({:.1}%)",
                                        format_bytes(inspect.ring_usage).bright_green(),
                                        ring_pct
                                    );
                                    let counters_pct = if balancer.total_usage > 0 {
                                        (inspect.counters_usage as f64 / balancer.total_usage as f64) * 100.0
                                    } else {
                                        0.0
                                    };
                                    println!(
                                        "          Counters: {} ({:.1}%)",
                                        format_bytes(inspect.counters_usage).bright_green(),
                                        counters_pct
                                    );
                                    if let Some(reals) = &inspect.reals_usage {
                                        let reals_pct = if balancer.total_usage > 0 {
                                            (reals.total_usage as f64 / balancer.total_usage as f64) * 100.0
                                        } else {
                                            0.0
                                        };
                                        println!(
                                            "          Reals: {} ({:.1}%)",
                                            format_bytes(reals.total_usage).bright_green(),
                                            reals_pct
                                        );
                                        let reals_counters_pct = if balancer.total_usage > 0 {
                                            (reals.counters_usage as f64 / balancer.total_usage as f64) * 100.0
                                        } else {
                                            0.0
                                        };
                                        println!(
                                            "            Counters: {} ({:.1}%)",
                                            format_bytes(reals.counters_usage).bright_green(),
                                            reals_counters_pct
                                        );
                                        let reals_data_pct = if balancer.total_usage > 0 {
                                            (reals.data_usage as f64 / balancer.total_usage as f64) * 100.0
                                        } else {
                                            0.0
                                        };
                                        println!(
                                            "            Data: {} ({:.1}%)",
                                            format_bytes(reals.data_usage).bright_green(),
                                            reals_data_pct
                                        );
                                    }
                                    let other_pct = if balancer.total_usage > 0 {
                                        (inspect.other_usage as f64 / balancer.total_usage as f64) * 100.0
                                    } else {
                                        0.0
                                    };
                                    println!(
                                        "          Other: {} ({:.1}%)",
                                        format_bytes(inspect.other_usage).bright_green(),
                                        other_pct
                                    );
                                }
                            }
                        }
                    }
                }
            }

            // Other packet handler components
            println!();
            let vs_index_pct = if balancer.total_usage > 0 {
                (ph.vs_index_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    VS Index: {} ({:.1}%)",
                format_bytes(ph.vs_index_usage).bright_green(),
                vs_index_pct
            );
            let reals_index_pct = if balancer.total_usage > 0 {
                (ph.reals_index_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    Reals Index: {} ({:.1}%)",
                format_bytes(ph.reals_index_usage).bright_green(),
                reals_index_pct
            );
            let counters_pct = if balancer.total_usage > 0 {
                (ph.counters_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    Counters: {} ({:.1}%)",
                format_bytes(ph.counters_usage).bright_green(),
                counters_pct
            );
            let decap_pct = if balancer.total_usage > 0 {
                (ph.decap_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    Decap: {} ({:.1}%)",
                format_bytes(ph.decap_usage).bright_green(),
                decap_pct
            );
        }

        // State breakdown
        if let Some(state) = &balancer.state_inspect {
            let state_percent = if balancer.total_usage > 0 {
                (state.total_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!();
            println!(
                "  {} {} ({:.1}%)",
                "State:".bright_cyan().bold(),
                format_bytes(state.total_usage).bright_green(),
                state_percent
            );
            let vs_reg_pct = if balancer.total_usage > 0 {
                (state.vs_registry_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    VS Registry: {} ({:.1}%)",
                format_bytes(state.vs_registry_usage).bright_green(),
                vs_reg_pct
            );
            let reals_reg_pct = if balancer.total_usage > 0 {
                (state.reals_registry_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    Reals Registry: {} ({:.1}%)",
                format_bytes(state.reals_registry_usage).bright_green(),
                reals_reg_pct
            );
            let session_pct = if balancer.total_usage > 0 {
                (state.session_table_usage as f64 / balancer.total_usage as f64) * 100.0
            } else {
                0.0
            };
            println!(
                "    Session Table: {} ({:.1}%)",
                format_bytes(state.session_table_usage).bright_green(),
                session_pct
            );
        }

        // Other usage
        let other_percent = if balancer.total_usage > 0 {
            (balancer.other_usage as f64 / balancer.total_usage as f64) * 100.0
        } else {
            0.0
        };
        println!();
        println!(
            "  {} {} ({:.1}%)",
            "Other:".bright_cyan().bold(),
            format_bytes(balancer.other_usage).cyan(),
            other_percent
        );
        println!();
    }

    Ok(())
}
