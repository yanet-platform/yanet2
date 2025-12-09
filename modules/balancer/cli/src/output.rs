
//! Output formatting for different display formats (JSON, Tree, Table)

use std::error::Error;
use chrono::{DateTime, Utc};
use colored::Colorize;
use ptree::TreeBuilder;
use tabled::{
    Table, Tabled,
    settings::Style,
};

use crate::rpc::balancerpb;
use crate::entities::{bytes_to_ip, format_bytes, format_number};
use crate::json_output;

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
        Ok(balancerpb::VsScheduler::Wrr) => "WRR".to_string(),
        Ok(balancerpb::VsScheduler::Prr) => "PRR".to_string(),
        Ok(balancerpb::VsScheduler::Wlc) => "WLC".to_string(),
        _ => format!("Unknown({})", sched),
    }
}

fn format_timestamp(ts: Option<&prost_types::Timestamp>) -> String {
    match ts {
        Some(ts) => {
            let dt = DateTime::<Utc>::from_timestamp(ts.seconds, ts.nanos as u32)
                .unwrap_or_default();
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
            if flags.gre { parts.push("gre"); }
            if flags.fix_mss { parts.push("mss"); }
            if flags.ops { parts.push("ops"); }
            if flags.pure_l3 { parts.push("l3"); }
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

    if let Some(target) = &response.target {
        tree.begin_child(format!("Config: {} (Instance: {})", target.config_name, target.dataplane_instance));
    }

    if let Some(config) = &response.module_config {
        tree.begin_child("Module Config".to_string());
        
        // Source addresses
        tree.begin_child("Source Addresses".to_string());
        if let Ok(ipv4) = bytes_to_ip(&config.source_address_v4) {
            tree.add_empty_child(format!("IPv4: {}", ipv4));
        }
        if let Ok(ipv6) = bytes_to_ip(&config.source_address_v6) {
            tree.add_empty_child(format!("IPv6: {}", ipv6));
        }
        tree.end_child();

        // Decap addresses
        if !config.decap_addresses.is_empty() {
            tree.begin_child("Decap Addresses".to_string());
            for addr in &config.decap_addresses {
                if let Ok(ip) = bytes_to_ip(addr) {
                    tree.add_empty_child(ip.to_string());
                }
            }
            tree.end_child();
        }

        // Timeouts
        if let Some(timeouts) = &config.sessions_timeouts {
            tree.begin_child("Session Timeouts".to_string());
            tree.add_empty_child(format!("TCP: {}s", timeouts.tcp));
            tree.add_empty_child(format!("TCP SYN: {}s", timeouts.tcp_syn));
            tree.add_empty_child(format!("TCP SYN-ACK: {}s", timeouts.tcp_syn_ack));
            tree.add_empty_child(format!("TCP FIN: {}s", timeouts.tcp_fin));
            tree.add_empty_child(format!("UDP: {}s", timeouts.udp));
            tree.add_empty_child(format!("Default: {}s", timeouts.default));
            tree.end_child();
        }

        // WLC
        if let Some(wlc) = &config.wlc {
            tree.begin_child("WLC Config".to_string());
            tree.add_empty_child(format!("Power: {}", wlc.wlc_power));
            tree.add_empty_child(format!("Max Real Weight: {}", wlc.max_real_weight));
            if let Some(period) = &wlc.update_period {
                tree.add_empty_child(format!("Update Period: {}ms", period.seconds * 1000 + period.nanos as i64 / 1_000_000));
            }
            tree.end_child();
        }

        // Virtual services
        tree.begin_child(format!("Virtual Services ({})", config.virtual_services.len()));
        for (idx, vs) in config.virtual_services.iter().enumerate() {
            if let Ok(ip) = bytes_to_ip(&vs.addr) {
                tree.begin_child(format!("[{}] {}:{}/{}", idx, ip, vs.port, proto_to_string(vs.proto)));
                tree.add_empty_child(format!("Scheduler: {}", scheduler_to_string(vs.scheduler)));
                tree.add_empty_child(format!("Flags: {}", format_flags(vs.flags.as_ref())));
                
                if !vs.allowed_srcs.is_empty() {
                    tree.begin_child("Allowed Sources".to_string());
                    for subnet in &vs.allowed_srcs {
                        if let Ok(ip) = bytes_to_ip(&subnet.addr) {
                            tree.add_empty_child(format!("{}/{}", ip, subnet.size));
                        }
                    }
                    tree.end_child();
                }

                if !vs.peers.is_empty() {
                    tree.begin_child("Peers".to_string());
                    for peer in &vs.peers {
                        if let Ok(ip) = bytes_to_ip(peer) {
                            tree.add_empty_child(ip.to_string());
                        }
                    }
                    tree.end_child();
                }

                tree.begin_child("Reals".to_string());
                for (ridx, real) in vs.reals.iter().enumerate() {
                    if let Ok(dst) = bytes_to_ip(&real.dst_addr) {
                        let enabled = if real.enabled { "✓" } else { "✗" };
                        tree.add_empty_child(format!("[{}] {}:{} weight={} enabled={}", ridx, dst, real.port, real.weight, enabled));
                    }
                }
                tree.end_child();

                tree.end_child();
            }
        }
        tree.end_child();

        tree.end_child();
    }

    if let Some(state_config) = &response.module_state_config {
        tree.begin_child("Module State Config".to_string());
        tree.add_empty_child(format!("Session Table Capacity: {}", format_number(state_config.session_table_capacity)));
        if let Some(period) = &state_config.session_table_scan_period {
            tree.add_empty_child(format!("Scan Period: {}ms", period.seconds * 1000 + period.nanos as i64 / 1_000_000));
        }
        tree.add_empty_child(format!("Max Load Factor: {:.2}", state_config.session_table_max_load_factor));
        tree.end_child();
    }

    if !response.buffered_real_updates.is_empty() {
        tree.begin_child(format!("Buffered Real Updates ({})", response.buffered_real_updates.len()));
        for update in &response.buffered_real_updates {
            if let (Ok(vip), Ok(rip)) = (bytes_to_ip(&update.virtual_ip), bytes_to_ip(&update.real_ip)) {
                let action = if update.enable { "enable" } else { "disable" };
                tree.add_empty_child(format!("{} {}:{}/{} -> {}", action, vip, update.port, proto_to_string(update.proto), rip));
            }
        }
        tree.end_child();
    }

    if let Some(_target) = &response.target {
        tree.end_child();
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_show_config_table(response: &balancerpb::ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    let subtitle = response.target.as_ref()
        .map(|t| format!("Config: {} | Instance: {}", t.config_name, t.dataplane_instance));
    print_boxed_header("BALANCER CONFIGURATION", subtitle.as_deref());
    println!();

    if let Some(config) = &response.module_config {
        // Source addresses
        println!("{}", "Source Addresses:".bright_cyan().bold());
        if let Ok(ipv4) = bytes_to_ip(&config.source_address_v4) {
            println!("  IPv4: {}", ipv4.to_string().bright_green());
        }
        if let Ok(ipv6) = bytes_to_ip(&config.source_address_v6) {
            println!("  IPv6: {}", ipv6.to_string().bright_green());
        }
        
        if !config.decap_addresses.is_empty() {
            let decap_ips: Vec<String> = config.decap_addresses.iter()
                .filter_map(|addr| bytes_to_ip(addr).ok().map(|ip| ip.to_string()))
                .collect();
            println!("  Decap: {}", decap_ips.join(", ").bright_green());
        }

        println!();
        
        // Timeouts
        if let Some(timeouts) = &config.sessions_timeouts {
            println!("{}", "Session Timeouts:".bright_cyan().bold());
            println!("  TCP: {}s | TCP SYN: {}s | TCP SYN-ACK: {}s | TCP FIN: {}s | UDP: {}s",
                timeouts.tcp.to_string().bright_green(),
                timeouts.tcp_syn.to_string().bright_green(),
                timeouts.tcp_syn_ack.to_string().bright_green(),
                timeouts.tcp_fin.to_string().bright_green(),
                timeouts.udp.to_string().bright_green()
            );
            println!();
        }

        // WLC
        if let Some(wlc) = &config.wlc {
            let period_ms = wlc.update_period.as_ref()
                .map(|p| p.seconds * 1000 + p.nanos as i64 / 1_000_000)
                .unwrap_or(0);
            println!("{}", "WLC Config:".bright_cyan().bold());
            println!("  Power: {} | Max Weight: {} | Update Period: {}ms",
                wlc.wlc_power.to_string().bright_green(),
                wlc.max_real_weight.to_string().bright_green(),
                period_ms.to_string().bright_green()
            );
            println!();
        }
    }

    // Virtual services table
    if let Some(config) = &response.module_config {
        if !config.virtual_services.is_empty() {
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
                #[tabled(rename = "Reals (enabled/total)")]
                reals: String,
            }

            let rows: Vec<VsRow> = config.virtual_services.iter().map(|vs| {
                let enabled_count = vs.reals.iter().filter(|r| r.enabled).count();
                VsRow {
                    ip: bytes_to_ip(&vs.addr).map(|ip| ip.to_string()).unwrap_or_default(),
                    port: vs.port.to_string(),
                    proto: proto_to_string(vs.proto),
                    scheduler: scheduler_to_string(vs.scheduler),
                    flags: format_flags(vs.flags.as_ref()),
                    reals: format!("{}/{}", enabled_count, vs.reals.len()),
                }
            }).collect();

            let table = Table::new(rows)
                .with(Style::rounded())
                .to_string();
            println!("{}", table);
            println!();

            // Print details for each VS
            for vs in &config.virtual_services {
                if let Ok(vs_ip) = bytes_to_ip(&vs.addr) {
                    println!("{}:", format!("VS {}:{}/{}", vs_ip, vs.port, proto_to_string(vs.proto)).bright_yellow().bold());
                    
                    #[derive(Tabled)]
                    struct RealRow {
                        #[tabled(rename = "Real IP")]
                        ip: String,
                        #[tabled(rename = "Weight")]
                        weight: String,
                        #[tabled(rename = "Enabled")]
                        enabled: String,
                        #[tabled(rename = "Source")]
                        source: String,
                        #[tabled(rename = "Source Mask")]
                        mask: String,
                    }

                    let real_rows: Vec<RealRow> = vs.reals.iter().map(|real| {
                        RealRow {
                            ip: bytes_to_ip(&real.dst_addr).map(|ip| ip.to_string()).unwrap_or_default(),
                            weight: real.weight.to_string(),
                            enabled: if real.enabled { "✓".to_string() } else { "✗".to_string() },
                            source: bytes_to_ip(&real.src_addr).map(|ip| ip.to_string()).unwrap_or_default(),
                            mask: bytes_to_ip(&real.src_mask).map(|ip| ip.to_string()).unwrap_or_default(),
                        }
                    }).collect();

                    let real_table = Table::new(real_rows)
                        .with(Style::rounded())
                        .to_string();
                    println!("{}", real_table);

                    // Peers
                    if !vs.peers.is_empty() {
                        let peer_ips: Vec<String> = vs.peers.iter()
                            .filter_map(|p| bytes_to_ip(p).ok().map(|ip| ip.to_string()))
                            .collect();
                        println!("{}: {}", "Peers".bright_cyan(), peer_ips.join(", "));
                    }

                    // Allowed sources
                    if !vs.allowed_srcs.is_empty() {
                        let srcs: Vec<String> = vs.allowed_srcs.iter()
                            .filter_map(|s| bytes_to_ip(&s.addr).ok().map(|ip| format!("{}/{}", ip, s.size)))
                            .collect();
                        println!("{}: {}", "Allowed Sources".bright_cyan(), srcs.join(", "));
                    }

                    println!();
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
            
            for config in &response.configs {
                // Use the same detailed tree structure as ShowConfig
                if let Some(target) = &config.target {
                    tree.begin_child(format!("Config: {} (Instance: {})", target.config_name, target.dataplane_instance));
                }

                if let Some(module_config) = &config.module_config {
                    tree.begin_child("Module Config".to_string());
                    
                    // Source addresses
                    tree.begin_child("Source Addresses".to_string());
                    if let Ok(ipv4) = bytes_to_ip(&module_config.source_address_v4) {
                        tree.add_empty_child(format!("IPv4: {}", ipv4));
                    }
                    if let Ok(ipv6) = bytes_to_ip(&module_config.source_address_v6) {
                        tree.add_empty_child(format!("IPv6: {}", ipv6));
                    }
                    tree.end_child();

                    // Decap addresses
                    if !module_config.decap_addresses.is_empty() {
                        tree.begin_child("Decap Addresses".to_string());
                        for addr in &module_config.decap_addresses {
                            if let Ok(ip) = bytes_to_ip(addr) {
                                tree.add_empty_child(ip.to_string());
                            }
                        }
                        tree.end_child();
                    }

                    // Timeouts
                    if let Some(timeouts) = &module_config.sessions_timeouts {
                        tree.begin_child("Session Timeouts".to_string());
                        tree.add_empty_child(format!("TCP: {}s", timeouts.tcp));
                        tree.add_empty_child(format!("TCP SYN: {}s", timeouts.tcp_syn));
                        tree.add_empty_child(format!("TCP SYN-ACK: {}s", timeouts.tcp_syn_ack));
                        tree.add_empty_child(format!("TCP FIN: {}s", timeouts.tcp_fin));
                        tree.add_empty_child(format!("UDP: {}s", timeouts.udp));
                        tree.add_empty_child(format!("Default: {}s", timeouts.default));
                        tree.end_child();
                    }

                    // WLC
                    if let Some(wlc) = &module_config.wlc {
                        tree.begin_child("WLC Config".to_string());
                        tree.add_empty_child(format!("Power: {}", wlc.wlc_power));
                        tree.add_empty_child(format!("Max Real Weight: {}", wlc.max_real_weight));
                        if let Some(period) = &wlc.update_period {
                            tree.add_empty_child(format!("Update Period: {}ms", period.seconds * 1000 + period.nanos as i64 / 1_000_000));
                        }
                        tree.end_child();
                    }

                    // Virtual services
                    tree.begin_child(format!("Virtual Services ({})", module_config.virtual_services.len()));
                    for (idx, vs) in module_config.virtual_services.iter().enumerate() {
                        if let Ok(ip) = bytes_to_ip(&vs.addr) {
                            tree.begin_child(format!("[{}] {}:{}/{}", idx, ip, vs.port, proto_to_string(vs.proto)));
                            tree.add_empty_child(format!("Scheduler: {}", scheduler_to_string(vs.scheduler)));
                            tree.add_empty_child(format!("Flags: {}", format_flags(vs.flags.as_ref())));
                            
                            if !vs.allowed_srcs.is_empty() {
                                tree.begin_child("Allowed Sources".to_string());
                                for subnet in &vs.allowed_srcs {
                                    if let Ok(ip) = bytes_to_ip(&subnet.addr) {
                                        tree.add_empty_child(format!("{}/{}", ip, subnet.size));
                                    }
                                }
                                tree.end_child();
                            }

                            if !vs.peers.is_empty() {
                                tree.begin_child("Peers".to_string());
                                for peer in &vs.peers {
                                    if let Ok(ip) = bytes_to_ip(peer) {
                                        tree.add_empty_child(ip.to_string());
                                    }
                                }
                                tree.end_child();
                            }

                            tree.begin_child("Reals".to_string());
                            for (ridx, real) in vs.reals.iter().enumerate() {
                                if let Ok(dst) = bytes_to_ip(&real.dst_addr) {
                                    let enabled = if real.enabled { "✓" } else { "✗" };
                                    tree.add_empty_child(format!("[{}] {}:{} weight={} enabled={}", ridx, dst, real.port, real.weight, enabled));
                                }
                            }
                            tree.end_child();

                            tree.end_child();
                        }
                    }
                    tree.end_child();

                    tree.end_child();
                }

                if let Some(state_config) = &config.module_state_config {
                    tree.begin_child("Module State Config".to_string());
                    tree.add_empty_child(format!("Session Table Capacity: {}", format_number(state_config.session_table_capacity)));
                    if let Some(period) = &state_config.session_table_scan_period {
                        tree.add_empty_child(format!("Scan Period: {}ms", period.seconds * 1000 + period.nanos as i64 / 1_000_000));
                    }
                    tree.add_empty_child(format!("Max Load Factor: {:.2}", state_config.session_table_max_load_factor));
                    tree.end_child();
                }

                if !config.buffered_real_updates.is_empty() {
                    tree.begin_child(format!("Buffered Real Updates ({})", config.buffered_real_updates.len()));
                    for update in &config.buffered_real_updates {
                        if let (Ok(vip), Ok(rip)) = (bytes_to_ip(&update.virtual_ip), bytes_to_ip(&update.real_ip)) {
                            let action = if update.enable { "enable" } else { "disable" };
                            tree.add_empty_child(format!("{} {}:{}/{} -> {}", action, vip, update.port, proto_to_string(update.proto), rip));
                        }
                    }
                    tree.end_child();
                }

                if config.target.is_some() {
                    tree.end_child();
                }
            }
            
            let tree = tree.build();
            ptree::print_tree(&tree)?;
        }
        OutputFormat::Table => {
            #[derive(Tabled)]
            struct ConfigRow {
                #[tabled(rename = "Config Name")]
                name: String,
                #[tabled(rename = "Instance")]
                instance: String,
                #[tabled(rename = "Virtual Services")]
                vs_count: String,
                #[tabled(rename = "Reals (enabled/total)")]
                reals_count: String,
            }

            let rows: Vec<ConfigRow> = response.configs.iter().map(|config| {
                let name = config.target.as_ref().map(|t| t.config_name.clone()).unwrap_or_default();
                let instance = config.target.as_ref().map(|t| t.dataplane_instance.to_string()).unwrap_or_default();
                let vs_count = config.module_config.as_ref().map(|c| c.virtual_services.len().to_string()).unwrap_or_default();
                let reals_count = config.module_config.as_ref()
                    .map(|c| {
                        let total: usize = c.virtual_services.iter()
                            .map(|vs| vs.reals.len())
                            .sum();
                        let enabled: usize = c.virtual_services.iter()
                            .flat_map(|vs| &vs.reals)
                            .filter(|r| r.enabled)
                            .count();
                        format!("{}/{}", enabled, total)
                    })
                    .unwrap_or_default();
                
                ConfigRow { name, instance, vs_count, reals_count }
            }).collect();

            let table = Table::new(rows)
                .with(Style::rounded())
                .to_string();
            println!("{}", table);
        }
    }
    Ok(())
}

////////////////////////////////////////////////////////////////////////////////
// StateInfo Output
////////////////////////////////////////////////////////////////////////////////

pub fn print_state_info(
    response: &balancerpb::StateInfoResponse,
    format: OutputFormat,
) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => {
            let json = json_output::convert_state_info(response);
            println!("{}", serde_json::to_string_pretty(&json)?);
        }
        OutputFormat::Tree => print_state_info_tree(response)?,
        OutputFormat::Table => print_state_info_table(response)?,
    }
    Ok(())
}

fn print_state_info_tree(response: &balancerpb::StateInfoResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Balancer State Info".to_string());

    if let Some(target) = &response.target {
        tree.begin_child(format!("Config: {} (Instance: {})", target.config_name, target.dataplane_instance));
    }

    if let Some(info) = &response.info {
        if let Some(active) = &info.active_sessions {
            tree.add_empty_child(format!("Active Sessions: {} (updated: {})",
                format_number(active.value),
                format_timestamp(active.updated_at.as_ref())
            ));
        }

        // Module stats
        if let Some(module) = &info.module {
            tree.begin_child("Module Stats".to_string());
            
            if let Some(common) = &module.common {
                tree.begin_child("Common".to_string());
                tree.add_empty_child(format!("Incoming: {} pkts, {}", format_number(common.incoming_packets), format_bytes(common.incoming_bytes)));
                tree.add_empty_child(format!("Outgoing: {} pkts, {}", format_number(common.outgoing_packets), format_bytes(common.outgoing_bytes)));
                tree.add_empty_child(format!("Unexpected Network Proto: {}", format_number(common.unexpected_network_proto)));
                tree.add_empty_child(format!("Decap Successful: {}", format_number(common.decap_successful)));
                tree.add_empty_child(format!("Decap Failed: {}", format_number(common.decap_failed)));
                tree.end_child();
            }

            if let Some(l4) = &module.l4 {
                tree.begin_child("L4".to_string());
                tree.add_empty_child(format!("Incoming Packets: {}", format_number(l4.incoming_packets)));
                tree.add_empty_child(format!("Outgoing Packets: {}", format_number(l4.outgoing_packets)));
                tree.add_empty_child(format!("Select VS Failed: {}", format_number(l4.select_vs_failed)));
                tree.add_empty_child(format!("Select Real Failed: {}", format_number(l4.select_real_failed)));
                tree.add_empty_child(format!("Invalid Packets: {}", format_number(l4.invalid_packets)));
                tree.end_child();
            }

            if let Some(icmpv4) = &module.icmpv4 {
                tree.begin_child("ICMPv4".to_string());
                tree.add_empty_child(format!("Incoming Packets: {}", format_number(icmpv4.incoming_packets)));
                tree.add_empty_child(format!("Echo Responses: {}", format_number(icmpv4.echo_responses)));
                tree.add_empty_child(format!("Payload Too Short IP: {}", format_number(icmpv4.payload_too_short_ip)));
                tree.add_empty_child(format!("Unmatching Src From Original: {}", format_number(icmpv4.unmatching_src_from_original)));
                tree.add_empty_child(format!("Payload Too Short Port: {}", format_number(icmpv4.payload_too_short_port)));
                tree.add_empty_child(format!("Unexpected Transport: {}", format_number(icmpv4.unexpected_transport)));
                tree.add_empty_child(format!("Unrecognized VS: {}", format_number(icmpv4.unrecognized_vs)));
                tree.add_empty_child(format!("Forwarded: {}", format_number(icmpv4.forwarded_packets)));
                tree.add_empty_child(format!("Broadcasted: {}", format_number(icmpv4.broadcasted_packets)));
                tree.add_empty_child(format!("Packet Clones Sent: {}", format_number(icmpv4.packet_clones_sent)));
                tree.add_empty_child(format!("Packet Clones Received: {}", format_number(icmpv4.packet_clones_received)));
                tree.add_empty_child(format!("Packet Clone Failures: {}", format_number(icmpv4.packet_clone_failures)));
                tree.end_child();
            }

            if let Some(icmpv6) = &module.icmpv6 {
                tree.begin_child("ICMPv6".to_string());
                tree.add_empty_child(format!("Incoming Packets: {}", format_number(icmpv6.incoming_packets)));
                tree.add_empty_child(format!("Echo Responses: {}", format_number(icmpv6.echo_responses)));
                tree.add_empty_child(format!("Payload Too Short IP: {}", format_number(icmpv6.payload_too_short_ip)));
                tree.add_empty_child(format!("Unmatching Src From Original: {}", format_number(icmpv6.unmatching_src_from_original)));
                tree.add_empty_child(format!("Payload Too Short Port: {}", format_number(icmpv6.payload_too_short_port)));
                tree.add_empty_child(format!("Unexpected Transport: {}", format_number(icmpv6.unexpected_transport)));
                tree.add_empty_child(format!("Unrecognized VS: {}", format_number(icmpv6.unrecognized_vs)));
                tree.add_empty_child(format!("Forwarded: {}", format_number(icmpv6.forwarded_packets)));
                tree.add_empty_child(format!("Broadcasted: {}", format_number(icmpv6.broadcasted_packets)));
                tree.add_empty_child(format!("Packet Clones Sent: {}", format_number(icmpv6.packet_clones_sent)));
                tree.add_empty_child(format!("Packet Clones Received: {}", format_number(icmpv6.packet_clones_received)));
                tree.add_empty_child(format!("Packet Clone Failures: {}", format_number(icmpv6.packet_clone_failures)));
                tree.end_child();
            }

            tree.end_child();
        }

        // VS info
        if !info.vs_info.is_empty() {
            tree.begin_child(format!("Virtual Services ({})", info.vs_info.len()));
            for vs in &info.vs_info {
                if let Ok(ip) = bytes_to_ip(&vs.vs_ip) {
                    tree.begin_child(format!("[{}] {}:{}/{}", vs.vs_registry_idx, ip, vs.vs_port, proto_to_string(vs.vs_proto)));
                    if let Some(active) = &vs.active_sessions {
                        tree.add_empty_child(format!("Active Sessions: {}", format_number(active.value)));
                    }
                    tree.add_empty_child(format!("Last Packet: {}", format_timestamp(vs.last_packet_timestamp.as_ref())));
                    
                    if let Some(stats) = &vs.stats {
                        tree.begin_child("Stats".to_string());
                        tree.add_empty_child(format!("Incoming: {} pkts, {}", format_number(stats.incoming_packets), format_bytes(stats.incoming_bytes)));
                        tree.add_empty_child(format!("Outgoing: {} pkts, {}", format_number(stats.outgoing_packets), format_bytes(stats.outgoing_bytes)));
                        tree.add_empty_child(format!("Created Sessions: {}", format_number(stats.created_sessions)));
                        tree.add_empty_child(format!("Packet Src Not Allowed: {}", format_number(stats.packet_src_not_allowed)));
                        tree.add_empty_child(format!("No Reals: {}", format_number(stats.no_reals)));
                        tree.add_empty_child(format!("OPS Packets: {}", format_number(stats.ops_packets)));
                        tree.add_empty_child(format!("Session Table Overflow: {}", format_number(stats.session_table_overflow)));
                        tree.add_empty_child(format!("Echo ICMP Packets: {}", format_number(stats.echo_icmp_packets)));
                        tree.add_empty_child(format!("Error ICMP Packets: {}", format_number(stats.error_icmp_packets)));
                        tree.add_empty_child(format!("Real Is Disabled: {}", format_number(stats.real_is_disabled)));
                        tree.add_empty_child(format!("Real Is Removed: {}", format_number(stats.real_is_removed)));
                        tree.add_empty_child(format!("Not Rescheduled Packets: {}", format_number(stats.not_rescheduled_packets)));
                        tree.add_empty_child(format!("Broadcasted ICMP Packets: {}", format_number(stats.broadcasted_icmp_packets)));
                        tree.end_child();
                    }
                    
                    tree.end_child();
                }
            }
            tree.end_child();
        }

        // Real info
        if !info.real_info.is_empty() {
            tree.begin_child(format!("Reals ({})", info.real_info.len()));
            for real in &info.real_info {
                if let (Ok(vs_ip), Ok(real_ip)) = (bytes_to_ip(&real.vs_ip), bytes_to_ip(&real.real_ip)) {
                    tree.begin_child(format!("[{}] {}:{}",
                        real.real_registry_idx, real_ip, real.vs_port));
                    tree.add_empty_child(format!("VS: {}:{}/{}", vs_ip, real.vs_port, proto_to_string(real.vs_proto)));
                    if let Some(active) = &real.active_sessions {
                        tree.add_empty_child(format!("Active Sessions: {}", format_number(active.value)));
                    }
                    tree.add_empty_child(format!("Last Packet: {}", format_timestamp(real.last_packet_timestamp.as_ref())));
                    
                    if let Some(stats) = &real.stats {
                        tree.begin_child("Stats".to_string());
                        tree.add_empty_child(format!("Packets: {}", format_number(stats.packets)));
                        tree.add_empty_child(format!("Bytes: {}", format_bytes(stats.bytes)));
                        tree.add_empty_child(format!("Created Sessions: {}", format_number(stats.created_sessions)));
                        tree.add_empty_child(format!("Packets Real Disabled: {}", format_number(stats.packets_real_disabled)));
                        tree.add_empty_child(format!("Packets Real Not Present: {}", format_number(stats.packets_real_not_present)));
                        tree.add_empty_child(format!("OPS Packets: {}", format_number(stats.ops_packets)));
                        tree.add_empty_child(format!("Error ICMP Packets: {}", format_number(stats.error_icmp_packets)));
                        tree.end_child();
                    }
                    
                    tree.end_child();
                }
            }
            tree.end_child();
        }
    }

    if let Some(_target) = &response.target {
        tree.end_child();
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_state_info_table(response: &balancerpb::StateInfoResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    let subtitle = if let (Some(target), Some(info)) = (&response.target, &response.info) {
        let active_sessions = info.active_sessions.as_ref()
            .map(|a| (format_number(a.value), format_timestamp(a.updated_at.as_ref())))
            .unwrap_or_else(|| ("0".to_string(), "N/A".to_string()));
        Some(format!("Config: {} | Instance: {} | Active Sessions: {} (updated: {})",
            target.config_name,
            target.dataplane_instance,
            active_sessions.0,
            active_sessions.1
        ))
    } else {
        None
    };
    print_boxed_header("BALANCER STATE INFO", subtitle.as_deref());
    println!();

    if let Some(info) = &response.info {
        // Module stats table
        if let Some(module) = &info.module {
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

            if let Some(common) = &module.common {
                rows.push(ModuleStatsRow {
                    category: "Common".to_string(),
                    metric: "Incoming".to_string(),
                    value: format!("{} pkts, {}", format_number(common.incoming_packets), format_bytes(common.incoming_bytes)),
                });
                rows.push(ModuleStatsRow {
                    category: "".to_string(),
                    metric: "Outgoing".to_string(),
                    value: format!("{} pkts, {}", format_number(common.outgoing_packets), format_bytes(common.outgoing_bytes)),
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

            if let Some(l4) = &module.l4 {
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

            if let Some(icmpv4) = &module.icmpv4 {
                rows.push(ModuleStatsRow {
                    category: "ICMPv4".to_string(),
                    metric: "Incoming Pkts".to_string(),
                    value: format_number(icmpv4.incoming_packets),
                });
                rows.push(ModuleStatsRow {
                    category: "".to_string(),
                    metric: "Echo Responses".to_string(),
                    value: format_number(icmpv4.echo_responses),
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

            if let Some(icmpv6) = &module.icmpv6 {
                rows.push(ModuleStatsRow {
                    category: "ICMPv6".to_string(),
                    metric: "Incoming Pkts".to_string(),
                    value: format_number(icmpv6.incoming_packets),
                });
                rows.push(ModuleStatsRow {
                    category: "".to_string(),
                    metric: "Echo Responses".to_string(),
                    value: format_number(icmpv6.echo_responses),
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

            let table = Table::new(rows)
                .with(Style::rounded())
                .to_string();
            println!("{}", table);
            println!();
        }

        // VS table
        if !info.vs_info.is_empty() {
            println!("{}", "Virtual Services:".bright_yellow().bold());
            
            let mut vs_list = info.vs_info.clone();
            vs_list.sort_by_key(|vs| vs.vs_registry_idx);

            #[derive(Tabled)]
            struct VsInfoRow {
                #[tabled(rename = "Index")]
                index: String,
                #[tabled(rename = "VS IP")]
                ip: String,
                #[tabled(rename = "Port")]
                port: String,
                #[tabled(rename = "Proto")]
                proto: String,
                #[tabled(rename = "Incoming")]
                incoming: String,
                #[tabled(rename = "Outgoing")]
                outgoing: String,
                #[tabled(rename = "Created Sessions")]
                created_sessions: String,
                #[tabled(rename = "Active Sessions")]
                sessions: String,
                #[tabled(rename = "Last Packet (UTC)")]
                last_packet: String,
            }

            let rows: Vec<VsInfoRow> = vs_list.iter().map(|vs| {
                VsInfoRow {
                    index: vs.vs_registry_idx.to_string(),
                    ip: bytes_to_ip(&vs.vs_ip).map(|ip| ip.to_string()).unwrap_or_default(),
                    port: vs.vs_port.to_string(),
                    proto: proto_to_string(vs.vs_proto),
                    incoming: vs.stats.as_ref()
                        .map(|s| format!("{} pkts, {}", format_number(s.incoming_packets), format_bytes(s.incoming_bytes)))
                        .unwrap_or_else(|| "0 pkts, 0 B".to_string()),
                    outgoing: vs.stats.as_ref()
                        .map(|s| format!("{} pkts, {}", format_number(s.outgoing_packets), format_bytes(s.outgoing_bytes)))
                        .unwrap_or_else(|| "0 pkts, 0 B".to_string()),
                    created_sessions: vs.stats.as_ref().map(|s| format_number(s.created_sessions)).unwrap_or_else(|| "0".to_string()),
                    sessions: vs.active_sessions.as_ref().map(|a| format_number(a.value)).unwrap_or_else(|| "0".to_string()),
                    last_packet: format_timestamp(vs.last_packet_timestamp.as_ref()),
                }
            }).collect();

            let table = Table::new(rows)
                .with(Style::rounded())
                .to_string();
            println!("{}", table);
            println!();
        }

        // Reals table
        if !info.real_info.is_empty() {
            println!("{}", "Reals:".bright_yellow().bold());
            
            let mut real_list = info.real_info.clone();
            real_list.sort_by_key(|r| r.real_registry_idx);

            #[derive(Tabled)]
            struct RealInfoRow {
                #[tabled(rename = "Index")]
                index: String,
                #[tabled(rename = "Real IP")]
                real_ip: String,
                #[tabled(rename = "Port")]
                real_port: String,
                #[tabled(rename = "VS IP")]
                vs_ip: String,
                #[tabled(rename = "Port")]
                vs_port: String,
                #[tabled(rename = "Proto")]
                proto: String,
                #[tabled(rename = "Traffic")]
                traffic: String,
                #[tabled(rename = "Created Sessions")]
                created_sessions: String,
                #[tabled(rename = "Active Sessions")]
                sessions: String,
                #[tabled(rename = "Last Packet (UTC)")]
                last_packet: String,
            }

            let rows: Vec<RealInfoRow> = real_list.iter().map(|real| {
                RealInfoRow {
                    index: real.real_registry_idx.to_string(),
                    real_ip: bytes_to_ip(&real.real_ip).map(|ip| ip.to_string()).unwrap_or_default(),
                    real_port: real.vs_port.to_string(), // Real port equals VS port
                    vs_ip: bytes_to_ip(&real.vs_ip).map(|ip| ip.to_string()).unwrap_or_default(),
                    vs_port: real.vs_port.to_string(),
                    proto: proto_to_string(real.vs_proto),
                    traffic: real.stats.as_ref()
                        .map(|s| format!("{} pkts, {}", format_number(s.packets), format_bytes(s.bytes)))
                        .unwrap_or_else(|| "0 pkts, 0 B".to_string()),
                    created_sessions: real.stats.as_ref().map(|s| format_number(s.created_sessions)).unwrap_or_else(|| "0".to_string()),
                    sessions: real.active_sessions.as_ref().map(|a| format_number(a.value)).unwrap_or_else(|| "0".to_string()),
                    last_packet: format_timestamp(real.last_packet_timestamp.as_ref()),
                }
            }).collect();

            let table = Table::new(rows)
                .with(Style::rounded())
                .to_string();
            println!("{}", table);
        }
    }

    Ok(())
}

////////////////////////////////////////////////////////////////////////////////
// ConfigStats Output
////////////////////////////////////////////////////////////////////////////////

pub fn print_config_stats(
    response: &balancerpb::ConfigStatsResponse,
    format: OutputFormat,
) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => {
            let json = json_output::convert_config_stats(response);
            println!("{}", serde_json::to_string_pretty(&json)?);
        }
        OutputFormat::Tree => print_config_stats_tree(response)?,
        OutputFormat::Table => print_config_stats_table(response)?,
    }
    Ok(())
}

fn print_config_stats_tree(response: &balancerpb::ConfigStatsResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Balancer Statistics".to_string());

    if let Some(target) = &response.target {
        tree.begin_child(format!("Config: {} (Instance: {})", target.config_name, target.dataplane_instance));
        tree.add_empty_child(format!("Device: {}", response.device));
        tree.add_empty_child(format!("Pipeline: {}", response.pipeline));
        tree.add_empty_child(format!("Function: {}", response.function));
        tree.add_empty_child(format!("Chain: {}", response.chain));
    }

    if let Some(stats) = &response.stats {
        if let Some(module) = &stats.module {
            tree.begin_child("Module Stats".to_string());
            
            if let Some(common) = &module.common {
                tree.begin_child("Common".to_string());
                tree.add_empty_child(format!("Incoming Packets: {}", format_number(common.incoming_packets)));
                tree.add_empty_child(format!("Incoming Bytes: {}", format_bytes(common.incoming_bytes)));
                tree.add_empty_child(format!("Outgoing Packets: {}", format_number(common.outgoing_packets)));
                tree.add_empty_child(format!("Outgoing Bytes: {}", format_bytes(common.outgoing_bytes)));
                tree.add_empty_child(format!("Decap Successful: {}", format_number(common.decap_successful)));
                tree.add_empty_child(format!("Decap Failed: {}", format_number(common.decap_failed)));
                tree.end_child();
            }

            if let Some(l4) = &module.l4 {
                tree.begin_child("L4".to_string());
                tree.add_empty_child(format!("Incoming Packets: {}", format_number(l4.incoming_packets)));
                tree.add_empty_child(format!("Outgoing Packets: {}", format_number(l4.outgoing_packets)));
                tree.add_empty_child(format!("Select VS Failed: {}", format_number(l4.select_vs_failed)));
                tree.add_empty_child(format!("Select Real Failed: {}", format_number(l4.select_real_failed)));
                tree.add_empty_child(format!("Invalid Packets: {}", format_number(l4.invalid_packets)));
                tree.end_child();
            }

            if let Some(icmpv4) = &module.icmpv4 {
                tree.begin_child("ICMPv4".to_string());
                tree.add_empty_child(format!("Incoming Packets: {}", format_number(icmpv4.incoming_packets)));
                tree.add_empty_child(format!("Echo Responses: {}", format_number(icmpv4.echo_responses)));
                tree.add_empty_child(format!("Forwarded: {}", format_number(icmpv4.forwarded_packets)));
                tree.add_empty_child(format!("Broadcasted: {}", format_number(icmpv4.broadcasted_packets)));
                tree.end_child();
            }

            if let Some(icmpv6) = &module.icmpv6 {
                tree.begin_child("ICMPv6".to_string());
                tree.add_empty_child(format!("Incoming Packets: {}", format_number(icmpv6.incoming_packets)));
                tree.add_empty_child(format!("Echo Responses: {}", format_number(icmpv6.echo_responses)));
                tree.add_empty_child(format!("Forwarded: {}", format_number(icmpv6.forwarded_packets)));
                tree.add_empty_child(format!("Broadcasted: {}", format_number(icmpv6.broadcasted_packets)));
                tree.end_child();
            }

            tree.end_child();
        }

        // VS stats
        if !stats.vs.is_empty() {
            tree.begin_child(format!("VS Stats ({})", stats.vs.len()));
            for vs in &stats.vs {
                if let Ok(ip) = bytes_to_ip(&vs.ip) {
                    tree.begin_child(format!("[{}] {}:{}/{}", vs.vs_registry_idx, ip, vs.port, proto_to_string(vs.proto)));
                    if let Some(s) = &vs.stats {
                        tree.add_empty_child(format!("Incoming: {} pkts, {}", format_number(s.incoming_packets), format_bytes(s.incoming_bytes)));
                        tree.add_empty_child(format!("Outgoing: {} pkts, {}", format_number(s.outgoing_packets), format_bytes(s.outgoing_bytes)));
                        tree.add_empty_child(format!("Created Sessions: {}", format_number(s.created_sessions)));
                        tree.add_empty_child(format!("Packet Src Not Allowed: {}", format_number(s.packet_src_not_allowed)));
                        tree.add_empty_child(format!("No Reals: {}", format_number(s.no_reals)));
                        tree.add_empty_child(format!("OPS Packets: {}", format_number(s.ops_packets)));
                        tree.add_empty_child(format!("Session Table Overflow: {}", format_number(s.session_table_overflow)));
                        tree.add_empty_child(format!("Echo ICMP Packets: {}", format_number(s.echo_icmp_packets)));
                        tree.add_empty_child(format!("Error ICMP Packets: {}", format_number(s.error_icmp_packets)));
                        tree.add_empty_child(format!("Real Is Disabled: {}", format_number(s.real_is_disabled)));
                        tree.add_empty_child(format!("Real Is Removed: {}", format_number(s.real_is_removed)));
                        tree.add_empty_child(format!("Not Rescheduled Packets: {}", format_number(s.not_rescheduled_packets)));
                        tree.add_empty_child(format!("Broadcasted ICMP Packets: {}", format_number(s.broadcasted_icmp_packets)));
                    }
                    tree.end_child();
                }
            }
            tree.end_child();
        }

        // Real stats
        if !stats.reals.is_empty() {
            tree.begin_child(format!("Real Stats ({})", stats.reals.len()));
            for real in &stats.reals {
                if let (Ok(vs_ip), Ok(real_ip)) = (bytes_to_ip(&real.vs_ip), bytes_to_ip(&real.real_ip)) {
                    tree.begin_child(format!("[{}] {} -> {}:{}/{}",
                        real.real_registry_idx, real_ip, vs_ip, real.port, proto_to_string(real.proto)));
                    if let Some(s) = &real.stats {
                        tree.add_empty_child(format!("Packets: {}", format_number(s.packets)));
                        tree.add_empty_child(format!("Bytes: {}", format_bytes(s.bytes)));
                        tree.add_empty_child(format!("Created Sessions: {}", format_number(s.created_sessions)));
                        tree.add_empty_child(format!("Packets Real Disabled: {}", format_number(s.packets_real_disabled)));
                        tree.add_empty_child(format!("Packets Real Not Present: {}", format_number(s.packets_real_not_present)));
                        tree.add_empty_child(format!("OPS Packets: {}", format_number(s.ops_packets)));
                        tree.add_empty_child(format!("Error ICMP Packets: {}", format_number(s.error_icmp_packets)));
                    }
                    tree.end_child();
                }
            }
            tree.end_child();
        }
    }

    if let Some(_target) = &response.target {
        tree.end_child();
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_config_stats_table(response: &balancerpb::ConfigStatsResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    let subtitle = response.target.as_ref()
        .map(|t| format!("Config: {} | Instance: {} | Device: {} | Pipeline: {} | Function: {} | Chain: {}",
            t.config_name,
            t.dataplane_instance,
            response.device,
            response.pipeline,
            response.function,
            response.chain
        ));
    print_boxed_header("BALANCER STATISTICS", subtitle.as_deref());
    println!();

    if let Some(stats) = &response.stats {
        if let Some(module) = &stats.module {
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

            if let Some(common) = &module.common {
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

            if let Some(l4) = &module.l4 {
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

            if let Some(icmpv4) = &module.icmpv4 {
                rows.push(ModuleStatsRow {
                    category: "ICMPv4".to_string(),
                    metric: "Incoming Pkts".to_string(),
                    value: format_number(icmpv4.incoming_packets),
                });
                rows.push(ModuleStatsRow {
                    category: "".to_string(),
                    metric: "Echo Responses".to_string(),
                    value: format_number(icmpv4.echo_responses),
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

            if let Some(icmpv6) = &module.icmpv6 {
                rows.push(ModuleStatsRow {
                    category: "ICMPv6".to_string(),
                    metric: "Incoming Pkts".to_string(),
                    value: format_number(icmpv6.incoming_packets),
                });
                rows.push(ModuleStatsRow {
                    category: "".to_string(),
                    metric: "Echo Responses".to_string(),
                    value: format_number(icmpv6.echo_responses),
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

            let table = Table::new(rows)
                .with(Style::rounded())
                .to_string();
            println!("{}", table);
            println!();
        }

        // VS Stats
        if !stats.vs.is_empty() {
            println!("{}", "VS Stats:".bright_yellow().bold());
            
            #[derive(Tabled)]
            struct VsStatsRow {
                #[tabled(rename = "Virtual IP")]
                ip: String,
                #[tabled(rename = "Port")]
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

            let rows: Vec<VsStatsRow> = stats.vs.iter().map(|vs| {
                let s = vs.stats.as_ref();
                VsStatsRow {
                    ip: bytes_to_ip(&vs.ip).map(|ip| ip.to_string()).unwrap_or_default(),
                    port: vs.port.to_string(),
                    proto: proto_to_string(vs.proto),
                    incoming: s.map(|s| format!("{} pkts, {}", format_number(s.incoming_packets), format_bytes(s.incoming_bytes))).unwrap_or_else(|| "0 pkts, 0 B".to_string()),
                    outgoing: s.map(|s| format!("{} pkts, {}", format_number(s.outgoing_packets), format_bytes(s.outgoing_bytes))).unwrap_or_else(|| "0 pkts, 0 B".to_string()),
                    sessions: s.map(|s| format_number(s.created_sessions)).unwrap_or_else(|| "0".to_string()),
                }
            }).collect();

            let table = Table::new(rows)
                .with(Style::rounded())
                .to_string();
            println!("{}", table);
            println!();
        }

        // Real Stats
        if !stats.reals.is_empty() {
            println!("{}", "Real Stats:".bright_yellow().bold());
            
            #[derive(Tabled)]
            struct RealStatsRow {
                #[tabled(rename = "Real IP")]
                real_ip: String,
                #[tabled(rename = "VS IP")]
                vs_ip: String,
                #[tabled(rename = "Port")]
                port: String,
                #[tabled(rename = "Proto")]
                proto: String,
                #[tabled(rename = "Traffic")]
                traffic: String,
                #[tabled(rename = "Created Sessions")]
                sessions: String,
            }

            let rows: Vec<RealStatsRow> = stats.reals.iter().map(|real| {
                let s = real.stats.as_ref();
                RealStatsRow {
                    real_ip: bytes_to_ip(&real.real_ip).map(|ip| ip.to_string()).unwrap_or_default(),
                    vs_ip: bytes_to_ip(&real.vs_ip).map(|ip| ip.to_string()).unwrap_or_default(),
                    port: real.port.to_string(),
                    proto: proto_to_string(real.proto),
                    traffic: s.map(|s| format!("{} pkts, {}", format_number(s.packets), format_bytes(s.bytes))).unwrap_or_else(|| "0 pkts, 0 B".to_string()),
                    sessions: s.map(|s| format_number(s.created_sessions)).unwrap_or_else(|| "0".to_string()),
                }
            }).collect();

            let table = Table::new(rows)
                .with(Style::rounded())
                .to_string();
            println!("{}", table);
        }
    }

    Ok(())
}

////////////////////////////////////////////////////////////////////////////////
// SessionsInfo Output
////////////////////////////////////////////////////////////////////////////////

pub fn print_sessions_info(
    response: &balancerpb::SessionsInfoResponse,
    format: OutputFormat,
) -> Result<(), Box<dyn Error>> {
    match format {
        OutputFormat::Json => {
            let json = json_output::convert_sessions_info(response);
            println!("{}", serde_json::to_string_pretty(&json)?);
        }
        OutputFormat::Tree => print_sessions_info_tree(response)?,
        OutputFormat::Table => print_sessions_info_table(response)?,
    }
    Ok(())
}

fn print_sessions_info_tree(response: &balancerpb::SessionsInfoResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Active Sessions".to_string());

    if let Some(target) = &response.target {
        tree.begin_child(format!("Config: {} (Instance: {})", target.config_name, target.dataplane_instance));
    }

    tree.add_empty_child(format!("Total Sessions: {}", format_number(response.sessions_info.len() as u64)));

    for (idx, session) in response.sessions_info.iter().enumerate() {
        if let (Ok(client), Ok(vs), Ok(real)) = (
            bytes_to_ip(&session.client_addr),
            bytes_to_ip(&session.vs_addr),
            bytes_to_ip(&session.real_addr)
        ) {
            tree.begin_child(format!("[{}] {}:{} -> {}:{} -> {}:{}",
                idx, client, session.client_port, vs, session.vs_port, real, session.real_port));
            tree.add_empty_child(format!("Created: {}", format_timestamp(session.create_timestamp.as_ref())));
            tree.add_empty_child(format!("Last Packet: {}", format_timestamp(session.last_packet_timestamp.as_ref())));
            tree.add_empty_child(format!("Timeout: {}", format_duration(session.timeout.as_ref())));
            tree.end_child();
        }
    }

    if let Some(_target) = &response.target {
        tree.end_child();
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;
    Ok(())
}

fn print_sessions_info_table(response: &balancerpb::SessionsInfoResponse) -> Result<(), Box<dyn Error>> {
    // Print header
    let subtitle = response.target.as_ref()
        .map(|t| format!("Config: {} | Instance: {} | Total Sessions: {}",
            t.config_name,
            t.dataplane_instance,
            format_number(response.sessions_info.len() as u64)
        ));
    print_boxed_header("ACTIVE SESSIONS", subtitle.as_deref());
    println!();

    if !response.sessions_info.is_empty() {
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
            #[tabled(rename = "Created (UTC)")]
            created: String,
            #[tabled(rename = "Last Packet (UTC)")]
            last_packet: String,
            #[tabled(rename = "Timeout")]
            timeout: String,
        }

        let rows: Vec<SessionRow> = response.sessions_info.iter().map(|session| {
            let client_ip = bytes_to_ip(&session.client_addr).map(|ip| ip.to_string()).unwrap_or_default();
            let vs_ip = bytes_to_ip(&session.vs_addr).map(|ip| ip.to_string()).unwrap_or_default();
            let real_ip = bytes_to_ip(&session.real_addr).map(|ip| ip.to_string()).unwrap_or_default();
            
            SessionRow {
                client: format!("{}:{}", client_ip, session.client_port),
                vs: format!("{}:{}", vs_ip, session.vs_port),
                real: format!("{}:{}", real_ip, session.real_port),
                proto: "TCP".to_string(), // Assuming TCP, not in proto
                created: format_timestamp(session.create_timestamp.as_ref()),
                last_packet: format_timestamp(session.last_packet_timestamp.as_ref()),
                timeout: format_duration(session.timeout.as_ref()),
            }
        }).collect();

        let table = Table::new(rows)
            .with(Style::rounded())
            .to_string();
        println!("{}", table);
    }

    Ok(())
}