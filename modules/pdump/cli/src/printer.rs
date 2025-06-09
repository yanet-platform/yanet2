use crate::pdumppb;
use pnet_packet::Packet;
use pnet_packet::ethernet::{EtherTypes, EthernetPacket};
use pnet_packet::icmp::{IcmpCode, IcmpPacket, IcmpTypes};
use pnet_packet::icmpv6::{Icmpv6Code, Icmpv6Packet, Icmpv6Types};
use pnet_packet::ip::{IpNextHeaderProtocol, IpNextHeaderProtocols};
use pnet_packet::ipv4::Ipv4Packet;
use pnet_packet::ipv6::Ipv6Packet;
use pnet_packet::tcp::TcpPacket;
use pnet_packet::udp::UdpPacket;
use pnet_packet::vlan::VlanPacket;
use std::io::{self, Write};
use std::net::{Ipv4Addr, Ipv6Addr};

/// Pretty prints a pdumppb::Record metadata field in a concise, single-line format.
///
/// # Arguments
///
/// * `writer` - A writer to output the formatted text to.
/// * `meta` - The metadata field from a pdumppb::Record.
pub fn pretty_print_metadata_concise<W: Write>(mut writer: W, meta: &pdumppb::RecordMeta) -> io::Result<()> {
    // Format: W:worker_idx P:pipeline_idx RX:rx_device_id TX:tx_device_id
    write!(
        writer,
        "{} Q:{} W:{} P:{} RX:{} TX:{} ",
        format_duration_ns(meta.timestamp),
        if meta.is_drops { "D" } else { "I" },
        meta.worker_idx,
        meta.pipeline_idx,
        meta.rx_device_id,
        meta.tx_device_id
    )
}

/// Pretty prints a pdumppb::Record metadata field in a detailed format.
///
/// # Arguments
///
/// * `writer` - A writer to output the formatted text to.
/// * `meta` - The metadata field from a pdumppb::Record.
pub fn pretty_print_metadata<W: Write>(mut writer: W, meta: &pdumppb::RecordMeta) -> io::Result<()> {
    let queue = if meta.is_drops { "DROPS" } else { "INPUT" };
    let ts = format_duration_ns(meta.timestamp);
    writeln!(writer, "--- Packet Metadata ---")?;
    writeln!(writer, "  Timestamp:      {}", ts)?;
    writeln!(writer, "  Packet Length:  {}", meta.packet_len)?;
    writeln!(writer, "  Worker Index:   {}", meta.worker_idx)?;
    writeln!(writer, "  Pipeline Index: {}", meta.pipeline_idx)?;
    writeln!(writer, "  Data Size:      {}", meta.data_size)?;
    writeln!(writer, "  RX Device ID:   {}", meta.rx_device_id)?;
    writeln!(writer, "  TX Device ID:   {}", meta.tx_device_id)?;
    writeln!(writer, "  Packet Queue:   {}", queue)?;
    writeln!(writer, "----------------------")?;
    Ok(())
}

/// Pretty prints an Ethernet frame in a concise, single-line format.
///
/// # Arguments
///
/// * `writer` - A writer to output the formatted text to.
/// * `ethernet_packet` - A byte slice representing the complete Ethernet frame.
pub fn pretty_print_ethernet_frame_concise<W: Write>(
    mut writer: W,
    ethernet_packet: &[u8],
    packet_len: u32,
) -> io::Result<()> {
    let frame = match EthernetPacket::new(ethernet_packet) {
        Some(frame) => frame,
        None => {
            writeln!(writer, "Error: Malformed Ethernet frame.")?;
            return Ok(());
        }
    };

    let mut current_ethertype = frame.get_ethertype();
    let mut current_payload = frame.payload().to_vec();
    let mut vlans = Vec::new();

    // Handle VLAN tags
    while current_ethertype == EtherTypes::Vlan {
        let Some(vlan_packet) = VlanPacket::new(&current_payload) else {
            writeln!(writer, "Error: Malformed VLAN tag.")?;
            return Ok(());
        };
        vlans.push(vlan_packet.get_vlan_identifier());
        current_ethertype = vlan_packet.get_ethertype();
        current_payload = vlan_packet.payload().to_vec();
    }

    // Extract L3/L4 protocol info
    let protocol_info = match current_ethertype {
        EtherTypes::Ipv4 => {
            if let Some(ipv4) = Ipv4Packet::new(&current_payload) {
                let l4_proto = match ipv4.get_next_level_protocol() {
                    IpNextHeaderProtocols::Tcp => {
                        if let Some(tcp) = TcpPacket::new(ipv4.payload()) {
                            let flags = get_tcp_flags_string(tcp.get_flags());
                            format!("TCP:{}->{}[{}]", tcp.get_source(), tcp.get_destination(), flags)
                        } else {
                            "TCP:malformed".to_string()
                        }
                    }
                    IpNextHeaderProtocols::Udp => {
                        if let Some(udp) = UdpPacket::new(ipv4.payload()) {
                            format!("UDP:{}->{}", udp.get_source(), udp.get_destination())
                        } else {
                            "UDP:malformed".to_string()
                        }
                    }
                    IpNextHeaderProtocols::Icmp => "ICMP".to_string(),
                    IpNextHeaderProtocols::Icmpv6 => "ICMPv6".to_string(),
                    _ => format!("Proto:{}", ipv4.get_next_level_protocol().0),
                };
                format!("IPv4:{}->{} {}", ipv4.get_source(), ipv4.get_destination(), l4_proto)
            } else {
                "IPv4:malformed".to_string()
            }
        }
        EtherTypes::Ipv6 => {
            if let Some(ipv6) = Ipv6Packet::new(&current_payload) {
                let l4_proto = match ipv6.get_next_header() {
                    IpNextHeaderProtocols::Tcp => {
                        if let Some(tcp) = TcpPacket::new(ipv6.payload()) {
                            let flags = get_tcp_flags_string(tcp.get_flags());
                            format!("TCP:{}->{}[{}]", tcp.get_source(), tcp.get_destination(), flags)
                        } else {
                            "TCP:malformed".to_string()
                        }
                    }
                    IpNextHeaderProtocols::Udp => {
                        if let Some(udp) = UdpPacket::new(ipv6.payload()) {
                            format!("UDP:{}->{}", udp.get_source(), udp.get_destination())
                        } else {
                            "UDP:malformed".to_string()
                        }
                    }
                    IpNextHeaderProtocols::Icmp => "ICMP".to_string(),
                    IpNextHeaderProtocols::Icmpv6 => "ICMPv6".to_string(),
                    _ => format!("Proto:{}", ipv6.get_next_header().0),
                };
                format!("IPv6:{}->{} {}", ipv6.get_source(), ipv6.get_destination(), l4_proto)
            } else {
                "IPv6:malformed".to_string()
            }
        }
        EtherTypes::Arp => "ARP".to_string(),
        _ => format!("EtherType:0x{:04x}", current_ethertype.0),
    };

    let mut vlan_part = vlans.iter().map(u16::to_string).collect::<Vec<String>>().join(",");
    if !vlan_part.is_empty() {
        vlan_part = format!(" VLANS[{}]", vlan_part);
    }
    let origin_len = if ethernet_packet.len() < packet_len as usize {
        format!("{}>", packet_len)
    } else {
        "".to_string()
    };
    // Print concise one-line summary
    writeln!(
        writer,
        "{}->{}{} {} ({}{}B)",
        frame.get_source(),
        frame.get_destination(),
        vlan_part,
        protocol_info,
        origin_len,
        ethernet_packet.len(),
    )?;

    Ok(())
}

/// Pretty prints an Ethernet frame.
///
/// # Arguments
///
/// * `writer` - A writer to output the formatted text to.
/// * `ethernet_packet` - A byte slice representing the complete Ethernet frame.
pub fn pretty_print_ethernet_frame<W: Write>(mut writer: W, ethernet_packet: &[u8], packet_len: u32) -> io::Result<()> {
    // Create an EthernetPacket from the byte slice.
    let frame = match EthernetPacket::new(ethernet_packet) {
        Some(frame) => frame,
        None => {
            writeln!(writer, "Error: Malformed Ethernet frame.")?;
            return Ok(());
        }
    };

    let truncated = if ethernet_packet.len() < packet_len as usize {
        format!(" (Truncated to {})", ethernet_packet.len())
    } else {
        "".to_string()
    };
    writeln!(writer, "--- Ethernet Frame {} Bytes{} ---", packet_len, truncated)?;
    writeln!(writer, "  Source MAC:      {}", frame.get_source())?;
    writeln!(writer, "  Destination MAC: {}", frame.get_destination())?;

    // The `ethertype` field tells us what kind of packet is encapsulated.
    let mut current_ethertype = frame.get_ethertype();
    let mut current_payload = frame.payload().to_vec();

    // Handle 802.1Q VLAN tags, which can be stacked.
    // A VLAN-tagged frame has an EtherType of 0x8100.
    while current_ethertype == EtherTypes::Vlan {
        let Some(vlan_packet) = VlanPacket::new(&current_payload) else {
            writeln!(writer, "Error: Malformed VLAN tag.")?;
            return Ok(());
        };

        // The VLAN packet's ethertype tells us what's next.
        current_ethertype = vlan_packet.get_ethertype();
        writeln!(writer, "  VLAN Tag:")?;
        writeln!(writer, "    Priority:      {}", vlan_packet.get_priority_code_point().0)?;
        writeln!(writer, "    Identifier:    {}", vlan_packet.get_vlan_identifier())?;

        current_payload = vlan_packet.payload().to_vec();
    }

    writeln!(
        writer,
        "  EtherType:       {} (0x{:04x})",
        ethertype_to_string(current_ethertype),
        current_ethertype.0
    )?;

    // Drill down into the next layer based on the EtherType.
    match current_ethertype {
        EtherTypes::Ipv4 => {
            pretty_print_ipv4_packet(&mut writer, &current_payload)?;
        }
        EtherTypes::Ipv6 => {
            pretty_print_ipv6_packet(&mut writer, &current_payload)?;
        }
        EtherTypes::Arp => {
            // ARP packet printing could be added here.
            writeln!(writer, "  L3 Protocol:     ARP (Not parsed)")?;
        }
        _ => {
            writeln!(writer, "  L3 Protocol:     (Unhandled EtherType)")?;
        }
    }
    writeln!(writer, "----------------------")?;

    Ok(())
}

fn pretty_print_ipv4_packet<W: Write>(mut writer: W, ipv4_packet: &[u8]) -> io::Result<()> {
    let packet = match Ipv4Packet::new(ipv4_packet) {
        Some(packet) => packet,
        None => {
            writeln!(writer, "Error: Malformed IPv4 packet.")?;
            return Ok(());
        }
    };

    writeln!(writer, "  --- IPv4 Packet ---")?;
    writeln!(writer, "    Version:         {}", packet.get_version())?;
    writeln!(writer, "    Header Length:   {} bytes", packet.get_header_length() * 4)?;
    writeln!(writer, "    Total Length:    {}", packet.get_total_length())?;
    writeln!(writer, "    TTL:             {}", packet.get_ttl())?;
    writeln!(writer, "    Source IP:       {}", packet.get_source())?;
    writeln!(writer, "    Destination IP:  {}", packet.get_destination())?;

    let next_protocol = packet.get_next_level_protocol();
    writeln!(
        writer,
        "    Protocol:        {} (0x{:02x})",
        ip_protocol_to_string(next_protocol),
        next_protocol.0
    )?;

    // Drill down into the transport layer (L4).
    handle_transport_protocol(&mut writer, next_protocol, packet.payload())?;

    Ok(())
}

fn pretty_print_ipv6_packet<W: Write>(mut writer: W, ipv6_packet: &[u8]) -> io::Result<()> {
    let packet = match Ipv6Packet::new(ipv6_packet) {
        Some(packet) => packet,
        None => {
            writeln!(writer, "Error: Malformed IPv6 packet.")?;
            return Ok(());
        }
    };

    writeln!(writer, "  --- IPv6 Packet ---")?;
    writeln!(writer, "    Version:         {}", packet.get_version())?;
    writeln!(writer, "    Payload Length:  {}", packet.get_payload_length())?;
    writeln!(writer, "    Hop Limit:       {}", packet.get_hop_limit())?;
    writeln!(writer, "    Source IP:       {}", packet.get_source())?;
    writeln!(writer, "    Destination IP:  {}", packet.get_destination())?;

    let next_header = packet.get_next_header();
    writeln!(
        writer,
        "    Next Header:     {} (0x{:02x})",
        ip_protocol_to_string(next_header),
        next_header.0
    )?;

    // Drill down into the transport layer (L4).
    handle_transport_protocol(&mut writer, next_header, packet.payload())?;

    Ok(())
}

fn handle_transport_protocol<W: Write>(
    mut writer: W,
    protocol: IpNextHeaderProtocol,
    payload: &[u8],
) -> io::Result<()> {
    match protocol {
        IpNextHeaderProtocols::Tcp => {
            pretty_print_tcp_packet(&mut writer, payload)?;
        }
        IpNextHeaderProtocols::Udp => {
            pretty_print_udp_packet(&mut writer, payload)?;
        }
        IpNextHeaderProtocols::Icmp => {
            pretty_print_icmp_packet(&mut writer, payload)?;
        }
        IpNextHeaderProtocols::Icmpv6 => {
            pretty_print_icmpv6_packet(&mut writer, payload)?;
        }
        _ => {
            writeln!(writer, "    L4 Protocol:     (Unhandled Protocol)")?;
        }
    }

    Ok(())
}

fn pretty_print_tcp_packet<W: Write>(mut writer: W, tcp_packet: &[u8]) -> io::Result<()> {
    let packet = match TcpPacket::new(tcp_packet) {
        Some(packet) => packet,
        None => {
            writeln!(writer, "Error: Malformed TCP packet.")?;
            return Ok(());
        }
    };

    writeln!(writer, "    --- TCP Segment ---")?;
    writeln!(writer, "      Source Port:      {}", packet.get_source())?;
    writeln!(writer, "      Destination Port: {}", packet.get_destination())?;
    writeln!(writer, "      Sequence Number:  {}", packet.get_sequence())?;
    writeln!(writer, "      Ack Number:       {}", packet.get_acknowledgement())?;
    writeln!(writer, "      Data Offset:      {} bytes", packet.get_data_offset() * 4)?;
    writeln!(writer, "      Window Size:      {}", packet.get_window())?;
    writeln!(writer, "      Checksum:         0x{:04x}", packet.get_checksum())?;

    // Print TCP flags
    let flags = packet.get_flags();
    let mut flag_strs = Vec::new();
    if (flags & pnet_packet::tcp::TcpFlags::FIN) != 0 {
        flag_strs.push("FIN");
    }
    if (flags & pnet_packet::tcp::TcpFlags::SYN) != 0 {
        flag_strs.push("SYN");
    }
    if (flags & pnet_packet::tcp::TcpFlags::RST) != 0 {
        flag_strs.push("RST");
    }
    if (flags & pnet_packet::tcp::TcpFlags::PSH) != 0 {
        flag_strs.push("PSH");
    }
    if (flags & pnet_packet::tcp::TcpFlags::ACK) != 0 {
        flag_strs.push("ACK");
    }
    if (flags & pnet_packet::tcp::TcpFlags::URG) != 0 {
        flag_strs.push("URG");
    }
    if (flags & pnet_packet::tcp::TcpFlags::ECE) != 0 {
        flag_strs.push("ECE");
    }
    if (flags & pnet_packet::tcp::TcpFlags::CWR) != 0 {
        flag_strs.push("CWR");
    }
    writeln!(writer, "      Flags:            [{}]", flag_strs.join(", "))?;
    writeln!(writer, "      Payload Length:   {} bytes", packet.payload().len())?;

    Ok(())
}

fn pretty_print_udp_packet<W: Write>(mut writer: W, udp_packet: &[u8]) -> io::Result<()> {
    let packet = match UdpPacket::new(udp_packet) {
        Some(packet) => packet,
        None => {
            writeln!(writer, "Error: Malformed UDP packet.")?;
            return Ok(());
        }
    };

    writeln!(writer, "    --- UDP Datagram ---")?;
    writeln!(writer, "      Source Port:      {}", packet.get_source())?;
    writeln!(writer, "      Destination Port: {}", packet.get_destination())?;
    writeln!(writer, "      Length:           {}", packet.get_length())?;
    writeln!(writer, "      Checksum:         0x{:04x}", packet.get_checksum())?;
    writeln!(writer, "      Payload Length:   {} bytes", packet.payload().len())?;

    Ok(())
}

/// Pretty prints an ICMP packet with detailed information.
///
/// # Arguments
///
/// * `icmp_packet` - A byte slice representing the ICMP packet payload.
fn pretty_print_icmp_packet<W: Write>(mut writer: W, icmp_packet: &[u8]) -> io::Result<()> {
    let packet = match IcmpPacket::new(icmp_packet) {
        Some(packet) => packet,
        None => {
            writeln!(
                writer,
                "      Error: Malformed ICMP packet (insufficient length: {} bytes)",
                icmp_packet.len()
            )?;
            return Ok(());
        }
    };

    writeln!(writer, "    --- ICMP Packet ---")?;

    let icmp_type = packet.get_icmp_type();
    let icmp_code = packet.get_icmp_code();

    writeln!(
        writer,
        "      Type:             {} ({})",
        icmp_type.0,
        icmp_type_to_string(icmp_type)
    )?;
    writeln!(writer, "      Code:             {}", icmp_code.0)?;
    writeln!(writer, "      Checksum:         0x{:04x}", packet.get_checksum())?;

    // Parse type-specific fields based on ICMP type
    match icmp_type {
        IcmpTypes::EchoReply | IcmpTypes::EchoRequest => {
            if packet.payload().len() >= 4 {
                let identifier = u16::from_be_bytes([packet.payload()[0], packet.payload()[1]]);
                let sequence = u16::from_be_bytes([packet.payload()[2], packet.payload()[3]]);
                writeln!(writer, "      Identifier:       {}", identifier)?;
                writeln!(writer, "      Sequence Number:  {}", sequence)?;
                writeln!(
                    writer,
                    "      Data Length:      {} bytes",
                    packet.payload().len().saturating_sub(4)
                )?;
            } else {
                writeln!(writer, "      Warning: Echo packet too short for identifier/sequence")?;
            }
        }
        IcmpTypes::DestinationUnreachable => {
            writeln!(
                writer,
                "      Reason:           {}",
                destination_unreachable_reason(icmp_code)
            )?;
            if packet.payload().len() >= 4 {
                writeln!(
                    writer,
                    "      Next-hop MTU:     {} (if applicable)",
                    u16::from_be_bytes([packet.payload()[2], packet.payload()[3]])
                )?;
            }
        }
        IcmpTypes::TimeExceeded => {
            writeln!(writer, "      Reason:           {}", time_exceeded_reason(icmp_code))?;
        }
        IcmpTypes::ParameterProblem => {
            if !packet.payload().is_empty() {
                writeln!(writer, "      Pointer:          {}", packet.payload()[0])?;
            }
        }
        IcmpTypes::RedirectMessage => {
            if packet.payload().len() >= 4 {
                let gateway = Ipv4Addr::new(
                    packet.payload()[0],
                    packet.payload()[1],
                    packet.payload()[2],
                    packet.payload()[3],
                );
                writeln!(writer, "      Gateway Address:  {}", gateway)?;
            }
        }
        _ => {
            // For other types, just show raw payload info
            if !packet.payload().is_empty() {
                writeln!(writer, "      Payload Length:   {} bytes", packet.payload().len())?;
            }
        }
    }

    // Show original packet data for error messages (if present and reasonable size)
    if matches!(
        icmp_type,
        IcmpTypes::DestinationUnreachable
            | IcmpTypes::TimeExceeded
            | IcmpTypes::ParameterProblem
            | IcmpTypes::RedirectMessage
    ) {
        let data_offset = match icmp_type {
            IcmpTypes::RedirectMessage => 4,
            IcmpTypes::ParameterProblem => 4,
            _ => 4,
        };

        if packet.payload().len() > data_offset {
            let original_data = &packet.payload()[data_offset..];
            writeln!(
                writer,
                "      Original Data:    {} bytes (truncated IP header + 8 bytes)",
                original_data.len()
            )?;
        }
    }

    Ok(())
}

/// Pretty prints an ICMPv6 packet with detailed information.
///
/// # Arguments
///
/// * `icmpv6_packet` - A byte slice representing the ICMPv6 packet payload.
fn pretty_print_icmpv6_packet<W: Write>(mut writer: W, icmpv6_packet: &[u8]) -> io::Result<()> {
    let packet = match Icmpv6Packet::new(icmpv6_packet) {
        Some(packet) => packet,
        None => {
            writeln!(
                writer,
                "      Error: Malformed ICMPv6 packet (insufficient length: {} bytes)",
                icmpv6_packet.len()
            )?;
            return Ok(());
        }
    };

    writeln!(writer, "    --- ICMPv6 Packet ---")?;

    let icmpv6_type = packet.get_icmpv6_type();
    let icmpv6_code = packet.get_icmpv6_code();

    writeln!(
        writer,
        "      Type:             {} ({})",
        icmpv6_type.0,
        icmpv6_type_to_string(icmpv6_type)
    )?;
    writeln!(writer, "      Code:             {}", icmpv6_code.0)?;
    writeln!(writer, "      Checksum:         0x{:04x}", packet.get_checksum())?;

    // Parse type-specific fields based on ICMPv6 type
    match icmpv6_type {
        Icmpv6Types::EchoRequest | Icmpv6Types::EchoReply => {
            if packet.payload().len() >= 4 {
                let identifier = u16::from_be_bytes([packet.payload()[0], packet.payload()[1]]);
                let sequence = u16::from_be_bytes([packet.payload()[2], packet.payload()[3]]);
                writeln!(writer, "      Identifier:       {}", identifier)?;
                writeln!(writer, "      Sequence Number:  {}", sequence)?;
                writeln!(
                    writer,
                    "      Data Length:      {} bytes",
                    packet.payload().len().saturating_sub(4)
                )?;
            } else {
                writeln!(writer, "      Warning: Echo packet too short for identifier/sequence")?;
            }
        }
        Icmpv6Types::DestinationUnreachable => {
            writeln!(
                writer,
                "      Reason:           {}",
                icmpv6_destination_unreachable_reason(icmpv6_code)
            )?;
            if packet.payload().len() >= 4 {
                writeln!(
                    writer,
                    "      MTU:              {} (if applicable)",
                    u32::from_be_bytes([
                        packet.payload()[0],
                        packet.payload()[1],
                        packet.payload()[2],
                        packet.payload()[3]
                    ])
                )?;
            }
        }
        Icmpv6Types::PacketTooBig => {
            if packet.payload().len() >= 4 {
                let mtu = u32::from_be_bytes([
                    packet.payload()[0],
                    packet.payload()[1],
                    packet.payload()[2],
                    packet.payload()[3],
                ]);
                writeln!(writer, "      MTU:              {}", mtu)?;
            }
        }
        Icmpv6Types::TimeExceeded => {
            writeln!(
                writer,
                "      Reason:           {}",
                icmpv6_time_exceeded_reason(icmpv6_code)
            )?;
        }
        Icmpv6Types::ParameterProblem => {
            if packet.payload().len() >= 4 {
                let pointer = u32::from_be_bytes([
                    packet.payload()[0],
                    packet.payload()[1],
                    packet.payload()[2],
                    packet.payload()[3],
                ]);
                writeln!(writer, "      Pointer:          {}", pointer)?;
            }
        }
        Icmpv6Types::RouterSolicit => {
            writeln!(
                writer,
                "      Reserved:         0x{:08x}",
                if packet.payload().len() >= 4 {
                    let mut addr: [u8; 4] = [0; 4];
                    addr.copy_from_slice(&packet.payload()[..4]);
                    u32::from_be_bytes(addr)
                } else {
                    0
                }
            )?;
        }
        Icmpv6Types::RouterAdvert => {
            if packet.payload().len() >= 4 {
                let flags = packet.payload()[0];
                let router_lifetime = u16::from_be_bytes([packet.payload()[2], packet.payload()[3]]);
                writeln!(writer, "      Managed Flag:     {}", (flags & 0x80) != 0)?;
                writeln!(writer, "      Other Flag:       {}", (flags & 0x40) != 0)?;
                writeln!(writer, "      Router Lifetime:  {} seconds", router_lifetime)?;
            }
            if packet.payload().len() >= 12 {
                let reachable_time = u32::from_be_bytes([
                    packet.payload()[4],
                    packet.payload()[5],
                    packet.payload()[6],
                    packet.payload()[7],
                ]);
                let retrans_timer = u32::from_be_bytes([
                    packet.payload()[8],
                    packet.payload()[9],
                    packet.payload()[10],
                    packet.payload()[11],
                ]);
                writeln!(writer, "      Reachable Time:   {} ms", reachable_time)?;
                writeln!(writer, "      Retrans Timer:    {} ms", retrans_timer)?;
            }
        }
        Icmpv6Types::NeighborSolicit => {
            if packet.payload().len() >= 20 {
                let mut addr: [u8; 16] = [0; 16];
                addr.copy_from_slice(&packet.payload()[4..19]);
                let target_addr = Ipv6Addr::from(addr);
                writeln!(writer, "      Target Address:   {}", target_addr)?;
            }
        }
        Icmpv6Types::NeighborAdvert => {
            if packet.payload().len() >= 4 {
                let flags = packet.payload()[0];
                writeln!(writer, "      Router Flag:      {}", (flags & 0x80) != 0)?;
                writeln!(writer, "      Solicited Flag:   {}", (flags & 0x40) != 0)?;
                writeln!(writer, "      Override Flag:    {}", (flags & 0x20) != 0)?;
            }
            if packet.payload().len() >= 20 {
                let mut addr: [u8; 16] = [0; 16];
                addr.copy_from_slice(&packet.payload()[4..19]);
                let target_addr = Ipv6Addr::from(addr);
                writeln!(writer, "      Target Address:   {}", target_addr)?;
            }
        }
        _ => {
            // For other types, just show raw payload info
            if !packet.payload().is_empty() {
                writeln!(writer, "      Payload Length:   {} bytes", packet.payload().len())?;
            }
        }
    }

    // Show original packet data for error messages (if present and reasonable size)
    if matches!(
        icmpv6_type,
        Icmpv6Types::DestinationUnreachable
            | Icmpv6Types::PacketTooBig
            | Icmpv6Types::TimeExceeded
            | Icmpv6Types::ParameterProblem
    ) {
        let data_offset = match icmpv6_type {
            Icmpv6Types::PacketTooBig | Icmpv6Types::ParameterProblem => 4,
            _ => 4,
        };

        if packet.payload().len() > data_offset {
            let original_data = &packet.payload()[data_offset..];
            writeln!(
                writer,
                "      Original Data:    {} bytes (truncated IPv6 header + payload)",
                original_data.len()
            )?;
        }
    }

    Ok(())
}

/// Converts ICMPv6 type to human-readable string.
fn icmpv6_type_to_string(icmpv6_type: pnet_packet::icmpv6::Icmpv6Type) -> &'static str {
    match icmpv6_type {
        Icmpv6Types::DestinationUnreachable => "Destination Unreachable",
        Icmpv6Types::PacketTooBig => "Packet Too Big",
        Icmpv6Types::TimeExceeded => "Time Exceeded",
        Icmpv6Types::ParameterProblem => "Parameter Problem",
        Icmpv6Types::EchoRequest => "Echo Request",
        Icmpv6Types::EchoReply => "Echo Reply",
        Icmpv6Types::RouterSolicit => "Router Solicitation",
        Icmpv6Types::RouterAdvert => "Router Advertisement",
        Icmpv6Types::NeighborSolicit => "Neighbor Solicitation",
        Icmpv6Types::NeighborAdvert => "Neighbor Advertisement",
        Icmpv6Types::Redirect => "Redirect",
        _ => "Unknown",
    }
}

/// Returns human-readable reason for ICMPv6 Destination Unreachable messages.
fn icmpv6_destination_unreachable_reason(code: Icmpv6Code) -> &'static str {
    match code.0 {
        0 => "No Route to Destination",
        1 => "Communication with Destination Administratively Prohibited",
        2 => "Beyond Scope of Source Address",
        3 => "Address Unreachable",
        4 => "Port Unreachable",
        5 => "Source Address Failed Ingress/Egress Policy",
        6 => "Reject Route to Destination",
        _ => "Unknown Reason",
    }
}

/// Returns human-readable reason for ICMPv6 Time Exceeded messages.
fn icmpv6_time_exceeded_reason(code: Icmpv6Code) -> &'static str {
    match code.0 {
        0 => "Hop Limit Exceeded in Transit",
        1 => "Fragment Reassembly Time Exceeded",
        _ => "Unknown Reason",
    }
}

/// Converts ICMP type to human-readable string.
fn icmp_type_to_string(icmp_type: pnet_packet::icmp::IcmpType) -> &'static str {
    match icmp_type {
        IcmpTypes::EchoReply => "Echo Reply",
        IcmpTypes::DestinationUnreachable => "Destination Unreachable",
        IcmpTypes::SourceQuench => "Source Quench",
        IcmpTypes::RedirectMessage => "Redirect",
        IcmpTypes::EchoRequest => "Echo Request",
        IcmpTypes::RouterAdvertisement => "Router Advertisement",
        IcmpTypes::RouterSolicitation => "Router Solicitation",
        IcmpTypes::TimeExceeded => "Time Exceeded",
        IcmpTypes::ParameterProblem => "Parameter Problem",
        IcmpTypes::Timestamp => "Timestamp Request",
        IcmpTypes::TimestampReply => "Timestamp Reply",
        IcmpTypes::InformationRequest => "Information Request",
        IcmpTypes::InformationReply => "Information Reply",
        _ => "Unknown",
    }
}

/// Returns human-readable reason for Destination Unreachable messages.
fn destination_unreachable_reason(code: IcmpCode) -> &'static str {
    match code.0 {
        0 => "Network Unreachable",
        1 => "Host Unreachable",
        2 => "Protocol Unreachable",
        3 => "Port Unreachable",
        4 => "Fragmentation Required but DF Set",
        5 => "Source Route Failed",
        6 => "Destination Network Unknown",
        7 => "Destination Host Unknown",
        8 => "Source Host Isolated",
        9 => "Network Administratively Prohibited",
        10 => "Host Administratively Prohibited",
        11 => "Network Unreachable for Service Type",
        12 => "Host Unreachable for Service Type",
        13 => "Communication Administratively Prohibited",
        14 => "Host Precedence Violation",
        15 => "Precedence Cutoff in Effect",
        _ => "Unknown Reason",
    }
}

/// Returns human-readable reason for Time Exceeded messages.
fn time_exceeded_reason(code: IcmpCode) -> &'static str {
    match code.0 {
        0 => "TTL Exceeded in Transit",
        1 => "Fragment Reassembly Time Exceeded",
        _ => "Unknown Reason",
    }
}

// --- Helper functions to convert protocol numbers to strings ---

fn ethertype_to_string(ethertype: pnet_packet::ethernet::EtherType) -> String {
    match ethertype {
        EtherTypes::Ipv4 => "IPv4".to_string(),
        EtherTypes::Ipv6 => "IPv6".to_string(),
        EtherTypes::Arp => "ARP".to_string(),
        EtherTypes::Vlan => "802.1Q VLAN".to_string(),
        _ => "Unknown".to_string(),
    }
}

fn ip_protocol_to_string(protocol: IpNextHeaderProtocol) -> String {
    match protocol {
        IpNextHeaderProtocols::Tcp => "TCP".to_string(),
        IpNextHeaderProtocols::Udp => "UDP".to_string(),
        IpNextHeaderProtocols::Icmp => "ICMP".to_string(),
        IpNextHeaderProtocols::Icmpv6 => "ICMPv6".to_string(),
        IpNextHeaderProtocols::Igmp => "IGMP".to_string(),
        _ => "Unknown".to_string(),
    }
}

/// Converts TCP flags to a compact string representation.
fn get_tcp_flags_string(flags: u8) -> String {
    let mut flag_strs = Vec::new();
    if (flags & pnet_packet::tcp::TcpFlags::FIN) != 0 {
        flag_strs.push("F");
    }
    if (flags & pnet_packet::tcp::TcpFlags::SYN) != 0 {
        flag_strs.push("S");
    }
    if (flags & pnet_packet::tcp::TcpFlags::RST) != 0 {
        flag_strs.push("R");
    }
    if (flags & pnet_packet::tcp::TcpFlags::PSH) != 0 {
        flag_strs.push("P");
    }
    if (flags & pnet_packet::tcp::TcpFlags::ACK) != 0 {
        flag_strs.push("A");
    }
    if (flags & pnet_packet::tcp::TcpFlags::URG) != 0 {
        flag_strs.push("U");
    }
    if (flags & pnet_packet::tcp::TcpFlags::ECE) != 0 {
        flag_strs.push("E");
    }
    if (flags & pnet_packet::tcp::TcpFlags::CWR) != 0 {
        flag_strs.push("C");
    }

    if flag_strs.is_empty() {
        "".to_string()
    } else {
        flag_strs.join("")
    }
}

fn format_duration_ns(ts: u64) -> String {
    let micro = ts / 1000 % 1000;
    let total_millis = ts / 1000000;
    let millis = total_millis % 1000;
    let total_seconds = total_millis / 1000;
    let seconds = total_seconds % 60;
    let total_minutes = total_seconds / 60;
    let minutes = total_minutes % 60;
    let hours = total_minutes / 60;

    format!("{:02}:{:02}:{:02}.{:03}.{:03}", hours, minutes, seconds, millis, micro)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Cursor;

    /// Helper function to create a basic Ethernet frame with IPv4 TCP packet
    fn create_ipv4_tcp_frame() -> Vec<u8> {
        let mut frame = Vec::new();

        // Ethernet header (14 bytes)
        frame.extend_from_slice(&[0x00, 0x11, 0x22, 0x33, 0x44, 0x55]); // Destination MAC
        frame.extend_from_slice(&[0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF]); // Source MAC
        frame.extend_from_slice(&[0x08, 0x00]); // EtherType: IPv4

        // IPv4 header (20 bytes minimum)
        frame.push(0x45); // Version (4) + IHL (5)
        frame.push(0x00); // DSCP + ECN
        frame.extend_from_slice(&[0x00, 0x3C]); // Total Length (60 bytes)
        frame.extend_from_slice(&[0x12, 0x34]); // Identification
        frame.extend_from_slice(&[0x40, 0x00]); // Flags + Fragment Offset
        frame.push(0x40); // TTL (64)
        frame.push(0x06); // Protocol: TCP
        frame.extend_from_slice(&[0x00, 0x00]); // Header Checksum (placeholder)
        frame.extend_from_slice(&[192, 168, 1, 100]); // Source IP
        frame.extend_from_slice(&[192, 168, 1, 200]); // Destination IP

        // TCP header (20 bytes minimum)
        frame.extend_from_slice(&[0x1F, 0x90]); // Source Port (8080)
        frame.extend_from_slice(&[0x00, 0x50]); // Destination Port (80)
        frame.extend_from_slice(&[0x12, 0x34, 0x56, 0x78]); // Sequence Number
        frame.extend_from_slice(&[0x87, 0x65, 0x43, 0x21]); // Acknowledgment Number
        frame.push(0x50); // Data Offset (5) + Reserved (0)
        frame.push(0x18); // Flags: PSH + ACK
        frame.extend_from_slice(&[0x20, 0x00]); // Window Size
        frame.extend_from_slice(&[0x00, 0x00]); // Checksum (placeholder)
        frame.extend_from_slice(&[0x00, 0x00]); // Urgent Pointer

        // Some payload data
        frame.extend_from_slice(b"YANET!!!");

        frame
    }

    /// Helper function to create a basic Ethernet frame with IPv4 UDP packet
    fn create_ipv4_udp_frame() -> Vec<u8> {
        let mut frame = Vec::new();

        // Ethernet header (14 bytes)
        frame.extend_from_slice(&[0x00, 0x11, 0x22, 0x33, 0x44, 0x55]); // Destination MAC
        frame.extend_from_slice(&[0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF]); // Source MAC
        frame.extend_from_slice(&[0x08, 0x00]); // EtherType: IPv4

        // IPv4 header (20 bytes minimum)
        frame.push(0x45); // Version (4) + IHL (5)
        frame.push(0x00); // DSCP + ECN
        frame.extend_from_slice(&[0x00, 0x24]); // Total Length (36 bytes)
        frame.extend_from_slice(&[0x12, 0x34]); // Identification
        frame.extend_from_slice(&[0x40, 0x00]); // Flags + Fragment Offset
        frame.push(0x40); // TTL (64)
        frame.push(0x11); // Protocol: UDP
        frame.extend_from_slice(&[0x00, 0x00]); // Header Checksum (placeholder)
        frame.extend_from_slice(&[10, 0, 0, 1]); // Source IP
        frame.extend_from_slice(&[10, 0, 0, 2]); // Destination IP

        // UDP header (8 bytes)
        frame.extend_from_slice(&[0x00, 0x35]); // Source Port (53 - DNS)
        frame.extend_from_slice(&[0x00, 0x35]); // Destination Port (53 - DNS)
        frame.extend_from_slice(&[0x00, 0x10]); // Length (16 bytes)
        frame.extend_from_slice(&[0x00, 0x00]); // Checksum (placeholder)

        // Some payload data
        frame.extend_from_slice(b"DNS_DATA");

        frame
    }

    /// Helper function to create an IPv6 TCP frame
    fn create_ipv6_tcp_frame() -> Vec<u8> {
        let mut frame = Vec::new();

        // Ethernet header (14 bytes)
        frame.extend_from_slice(&[0x00, 0x11, 0x22, 0x33, 0x44, 0x55]); // Destination MAC
        frame.extend_from_slice(&[0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF]); // Source MAC
        frame.extend_from_slice(&[0x86, 0xDD]); // EtherType: IPv6

        // IPv6 header (40 bytes)
        frame.push(0x60); // Version (6) + Traffic Class (0)
        frame.extend_from_slice(&[0x00, 0x00, 0x00]); // Traffic Class + Flow Label
        frame.extend_from_slice(&[0x00, 0x20]); // Payload Length (32 bytes)
        frame.push(0x06); // Next Header: TCP
        frame.push(0x40); // Hop Limit (64)
        // Source IPv6 address (16 bytes)
        frame.extend_from_slice(&[
            0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
        ]);
        // Destination IPv6 address (16 bytes)
        frame.extend_from_slice(&[
            0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02,
        ]);

        // TCP header (20 bytes minimum)
        frame.extend_from_slice(&[0x1F, 0x90]); // Source Port (8080)
        frame.extend_from_slice(&[0x01, 0xBB]); // Destination Port (443)
        frame.extend_from_slice(&[0x12, 0x34, 0x56, 0x78]); // Sequence Number
        frame.extend_from_slice(&[0x87, 0x65, 0x43, 0x21]); // Acknowledgment Number
        frame.push(0x50); // Data Offset (5) + Reserved (0)
        frame.push(0x02); // Flags: SYN
        frame.extend_from_slice(&[0x20, 0x00]); // Window Size
        frame.extend_from_slice(&[0x00, 0x00]); // Checksum (placeholder)
        frame.extend_from_slice(&[0x00, 0x00]); // Urgent Pointer

        // Some payload data
        frame.extend_from_slice(b"IPv6 Data!");

        frame
    }

    /// Helper function to create a VLAN-tagged frame
    fn create_vlan_frame() -> Vec<u8> {
        let mut frame = Vec::new();

        // Ethernet header (14 bytes)
        frame.extend_from_slice(&[0x00, 0x11, 0x22, 0x33, 0x44, 0x55]); // Destination MAC
        frame.extend_from_slice(&[0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF]); // Source MAC
        frame.extend_from_slice(&[0x81, 0x00]); // EtherType: 802.1Q VLAN

        // VLAN tag (4 bytes)
        frame.extend_from_slice(&[0x20, 0x64]); // Priority (1) + VLAN ID (100)
        frame.extend_from_slice(&[0x08, 0x06]); // EtherType: ARP

        // ARP packet (28 bytes minimum)
        frame.extend_from_slice(&[0x00, 0x01]); // Hardware Type: Ethernet
        frame.extend_from_slice(&[0x08, 0x00]); // Protocol Type: IPv4
        frame.push(0x06); // Hardware Address Length
        frame.push(0x04); // Protocol Address Length
        frame.extend_from_slice(&[0x00, 0x01]); // Operation: Request
        frame.extend_from_slice(&[0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF]); // Sender MAC
        frame.extend_from_slice(&[192, 168, 1, 1]); // Sender IP
        frame.extend_from_slice(&[0x00, 0x00, 0x00, 0x00, 0x00, 0x00]); // Target MAC
        frame.extend_from_slice(&[192, 168, 1, 2]); // Target IP

        frame
    }

    /// Helper function to create a malformed frame (too short)
    fn create_malformed_frame() -> Vec<u8> {
        vec![0x00, 0x11, 0x22] // Too short to be a valid Ethernet frame
    }

    /// Helper function to create an ARP frame
    fn create_arp_frame() -> Vec<u8> {
        let mut frame = Vec::new();

        // Ethernet header (14 bytes)
        frame.extend_from_slice(&[0x00, 0x11, 0x22, 0x33, 0x44, 0x55]); // Destination MAC
        frame.extend_from_slice(&[0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF]); // Source MAC
        frame.extend_from_slice(&[0x08, 0x06]); // EtherType: ARP

        // ARP packet (28 bytes minimum)
        frame.extend_from_slice(&[0x00, 0x01]); // Hardware Type: Ethernet
        frame.extend_from_slice(&[0x08, 0x00]); // Protocol Type: IPv4
        frame.push(0x06); // Hardware Address Length
        frame.push(0x04); // Protocol Address Length
        frame.extend_from_slice(&[0x00, 0x02]); // Operation: Reply
        frame.extend_from_slice(&[0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF]); // Sender MAC
        frame.extend_from_slice(&[192, 168, 1, 1]); // Sender IP
        frame.extend_from_slice(&[0x00, 0x11, 0x22, 0x33, 0x44, 0x55]); // Target MAC
        frame.extend_from_slice(&[192, 168, 1, 2]); // Target IP

        frame
    }

    #[test]
    fn test_print_ethernet_frame_concise_ipv4_tcp() {
        let frame = create_ipv4_tcp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame_concise(&mut output, &frame, 0).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that output contains expected elements
        assert!(output_str.contains("aa:bb:cc:dd:ee:ff->00:11:22:33:44:55"));
        assert!(output_str.contains("IPv4:192.168.1.100->192.168.1.200"));
        assert!(output_str.contains("TCP:8080->80"));
        assert!(output_str.contains("PA")); // PSH + ACK flags
        assert!(output_str.contains(&format!("({}B)", frame.len())));
    }

    #[test]
    fn test_print_ethernet_frame_concise_ipv4_udp() {
        let frame = create_ipv4_udp_frame();
        let mut output = Vec::new();

        let packet_len = frame.len() * 2;
        pretty_print_ethernet_frame_concise(&mut output, &frame, packet_len as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that output contains expected elements
        assert!(output_str.contains("aa:bb:cc:dd:ee:ff->00:11:22:33:44:55"));
        assert!(output_str.contains("IPv4:10.0.0.1->10.0.0.2"));
        assert!(output_str.contains("UDP:53->53"));
        assert!(output_str.contains(&format!("({}>{}B)", packet_len, frame.len())));
    }

    #[test]
    fn test_print_ethernet_frame_concise_ipv6_tcp() {
        let frame = create_ipv6_tcp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame_concise(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that output contains expected elements
        assert!(output_str.contains("aa:bb:cc:dd:ee:ff->00:11:22:33:44:55"));
        assert!(output_str.contains("IPv6:2001:db8::1->2001:db8::2"));
        assert!(output_str.contains("TCP:8080->443"));
        assert!(output_str.contains("S")); // SYN flag
        assert!(output_str.contains(&format!("({}B)", frame.len())));
    }

    #[test]
    fn test_print_ethernet_frame_concise_vlan() {
        let frame = create_vlan_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame_concise(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that output contains expected elements
        assert!(output_str.contains("aa:bb:cc:dd:ee:ff->00:11:22:33:44:55"));
        assert!(output_str.contains("VLANS[100]"));
        assert!(output_str.contains("ARP"));
        assert!(output_str.contains(&format!("({}B)", frame.len())));
    }

    #[test]
    fn test_print_ethernet_frame_concise_arp() {
        let frame = create_arp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame_concise(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that output contains expected elements
        assert!(output_str.contains("aa:bb:cc:dd:ee:ff->00:11:22:33:44:55"));
        assert!(output_str.contains("ARP"));
        assert!(output_str.contains(&format!("({}B)", frame.len())));
    }

    #[test]
    fn test_print_ethernet_frame_concise_malformed() {
        let frame = create_malformed_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame_concise(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that error message is printed
        assert!(output_str.contains("Error: Malformed Ethernet frame."));
    }

    #[test]
    fn test_pretty_print_ethernet_frame_ipv4_tcp() {
        let frame = create_ipv4_tcp_frame();
        let mut output = Vec::new();

        let packet_len = frame.len() as u32 * 3;
        pretty_print_ethernet_frame(&mut output, &frame, packet_len).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that output contains expected sections and data
        assert!(
            output_str.contains("--- Ethernet Frame 186 Bytes (Truncated to 62) ---"),
            "{}",
            output_str
        );
        assert!(output_str.contains("Source MAC:      aa:bb:cc:dd:ee:ff"));
        assert!(output_str.contains("Destination MAC: 00:11:22:33:44:55"));
        assert!(output_str.contains("EtherType:       IPv4 (0x0800)"));
        assert!(output_str.contains("--- IPv4 Packet ---"));
        assert!(output_str.contains("Version:         4"));
        assert!(output_str.contains("Source IP:       192.168.1.100"));
        assert!(output_str.contains("Destination IP:  192.168.1.200"));
        assert!(output_str.contains("Protocol:        TCP (0x06)"));
        assert!(output_str.contains("--- TCP Segment ---"));
        assert!(output_str.contains("Source Port:      8080"));
        assert!(output_str.contains("Destination Port: 80"));
        assert!(output_str.contains("Flags:            [PSH, ACK]"));
        assert!(output_str.contains("----------------------"));
    }

    #[test]
    fn test_pretty_print_ethernet_frame_ipv4_udp() {
        let frame = create_ipv4_udp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that output contains expected sections and data
        assert!(output_str.contains("--- Ethernet Frame 50 Bytes ---"), "{}", output_str);
        assert!(output_str.contains("Source MAC:      aa:bb:cc:dd:ee:ff"));
        assert!(output_str.contains("Destination MAC: 00:11:22:33:44:55"));
        assert!(output_str.contains("EtherType:       IPv4 (0x0800)"));
        assert!(output_str.contains("--- IPv4 Packet ---"));
        assert!(output_str.contains("Source IP:       10.0.0.1"));
        assert!(output_str.contains("Destination IP:  10.0.0.2"));
        assert!(output_str.contains("Protocol:        UDP (0x11)"));
        assert!(output_str.contains("--- UDP Datagram ---"));
        assert!(output_str.contains("Source Port:      53"));
        assert!(output_str.contains("Destination Port: 53"));
        assert!(output_str.contains("----------------------"));
    }

    #[test]
    fn test_pretty_print_ethernet_frame_ipv6_tcp() {
        let frame = create_ipv6_tcp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that output contains expected sections and data
        assert!(output_str.contains("--- Ethernet Frame 84 Bytes ---"), "{}", output_str);
        assert!(output_str.contains("Source MAC:      aa:bb:cc:dd:ee:ff"));
        assert!(output_str.contains("Destination MAC: 00:11:22:33:44:55"));
        assert!(output_str.contains("EtherType:       IPv6 (0x86dd)"));
        assert!(output_str.contains("--- IPv6 Packet ---"));
        assert!(output_str.contains("Version:         6"));
        assert!(output_str.contains("Source IP:       2001:db8::1"));
        assert!(output_str.contains("Destination IP:  2001:db8::2"));
        assert!(output_str.contains("Next Header:     TCP (0x06)"));
        assert!(output_str.contains("--- TCP Segment ---"));
        assert!(output_str.contains("Source Port:      8080"));
        assert!(output_str.contains("Destination Port: 443"));
        assert!(output_str.contains("Flags:            [SYN]"));
        assert!(output_str.contains("----------------------"));
    }

    #[test]
    fn test_pretty_print_ethernet_frame_vlan() {
        let frame = create_vlan_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that output contains expected sections and data
        assert!(output_str.contains("--- Ethernet Frame 46 Bytes ---"), "{}", output_str);
        assert!(output_str.contains("Source MAC:      aa:bb:cc:dd:ee:ff"));
        assert!(output_str.contains("Destination MAC: 00:11:22:33:44:55"));
        assert!(output_str.contains("VLAN Tag:"));
        assert!(output_str.contains("Priority:      1"));
        assert!(output_str.contains("Identifier:    100"));
        assert!(output_str.contains("EtherType:       ARP (0x0806)"));
        assert!(output_str.contains("L3 Protocol:     ARP (Not parsed)"));
        assert!(output_str.contains("----------------------"));
    }

    #[test]
    fn test_pretty_print_ethernet_frame_arp() {
        let frame = create_arp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that output contains expected sections and data
        assert!(output_str.contains("--- Ethernet Frame 42 Bytes ---"), "{}", output_str);
        assert!(output_str.contains("Source MAC:      aa:bb:cc:dd:ee:ff"));
        assert!(output_str.contains("Destination MAC: 00:11:22:33:44:55"));
        assert!(output_str.contains("EtherType:       ARP (0x0806)"));
        assert!(output_str.contains("L3 Protocol:     ARP (Not parsed)"));
        assert!(output_str.contains("----------------------"));
    }

    #[test]
    fn test_pretty_print_ethernet_frame_malformed() {
        let frame = create_malformed_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that error message is printed
        assert!(output_str.contains("Error: Malformed Ethernet frame."));
    }

    #[test]
    fn test_pretty_print_ethernet_frame_unknown_ethertype() {
        let mut frame = Vec::new();

        // Ethernet header with unknown EtherType
        frame.extend_from_slice(&[0x00, 0x11, 0x22, 0x33, 0x44, 0x55]); // Destination MAC
        frame.extend_from_slice(&[0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF]); // Source MAC
        frame.extend_from_slice(&[0xFF, 0xFF]); // Unknown EtherType

        let mut output = Vec::new();
        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that unknown EtherType is handled
        assert!(output_str.contains("--- Ethernet Frame 14 Bytes ---"), "{}", output_str);
        assert!(output_str.contains("EtherType:       Unknown (0xffff)"));
        assert!(output_str.contains("L3 Protocol:     (Unhandled EtherType)"));
        assert!(output_str.contains("----------------------"));
    }

    #[test]
    fn test_print_ethernet_frame_concise_unknown_ethertype() {
        let mut frame = Vec::new();

        // Ethernet header with unknown EtherType
        frame.extend_from_slice(&[0x00, 0x11, 0x22, 0x33, 0x44, 0x55]); // Destination MAC
        frame.extend_from_slice(&[0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF]); // Source MAC
        frame.extend_from_slice(&[0x12, 0x34]); // Unknown EtherType

        let mut output = Vec::new();
        let packet_len = frame.len() as u32 * 3;
        pretty_print_ethernet_frame_concise(&mut output, &frame, packet_len).unwrap();

        let output_str = String::from_utf8(output).unwrap();

        // Check that unknown EtherType is handled in concise format
        assert!(output_str.contains("aa:bb:cc:dd:ee:ff->00:11:22:33:44:55"));
        assert!(output_str.contains("EtherType:0x1234"));
        assert!(output_str.contains("(42>14B)"), "{}", output_str);
    }

    #[test]
    fn test_print_ethernet_frame_concise_with_cursor() {
        let frame = create_ipv4_tcp_frame();
        let mut cursor = Cursor::new(Vec::new());

        pretty_print_ethernet_frame_concise(&mut cursor, &frame, frame.len() as u32).unwrap();

        let output = cursor.into_inner();
        let output_str = String::from_utf8(output).unwrap();

        // Verify the function works with Cursor as well
        assert!(output_str.contains("aa:bb:cc:dd:ee:ff->00:11:22:33:44:55"));
        assert!(output_str.contains("IPv4:192.168.1.100->192.168.1.200"));
        assert!(output_str.contains("TCP:8080->80"));
    }

    #[test]
    fn test_pretty_print_ethernet_frame_with_cursor() {
        let frame = create_ipv4_udp_frame();
        let mut cursor = Cursor::new(Vec::new());

        pretty_print_ethernet_frame(&mut cursor, &frame, frame.len() as u32).unwrap();

        let output = cursor.into_inner();
        let output_str = String::from_utf8(output).unwrap();

        // Verify the function works with Cursor as well
        assert!(output_str.contains("--- Ethernet Frame 50 Bytes ---"), "{}", output_str);
        assert!(output_str.contains("Source MAC:      aa:bb:cc:dd:ee:ff"));
        assert!(output_str.contains("--- UDP Datagram ---"));
        assert!(output_str.contains("----------------------"));
    }

    #[test]
    fn test_print_ethernet_frame_concise_full_output() {
        let frame = create_ipv4_tcp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame_concise(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = format!(
            "aa:bb:cc:dd:ee:ff->00:11:22:33:44:55 IPv4:192.168.1.100->192.168.1.200 TCP:8080->80[PA] ({}B)\n",
            frame.len()
        );

        assert_eq!(output_str, expected);
    }

    #[test]
    fn test_print_ethernet_frame_concise_udp_full_output() {
        let frame = create_ipv4_udp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame_concise(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = format!(
            "aa:bb:cc:dd:ee:ff->00:11:22:33:44:55 IPv4:10.0.0.1->10.0.0.2 UDP:53->53 ({}B)\n",
            frame.len()
        );

        assert_eq!(output_str, expected);
    }

    #[test]
    fn test_print_ethernet_frame_concise_vlan_full_output() {
        let frame = create_vlan_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame_concise(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = format!(
            "aa:bb:cc:dd:ee:ff->00:11:22:33:44:55 VLANS[100] ARP ({}B)\n",
            frame.len()
        );

        assert_eq!(output_str, expected);
    }

    #[test]
    fn test_print_ethernet_frame_concise_arp_full_output() {
        let frame = create_arp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame_concise(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = format!("aa:bb:cc:dd:ee:ff->00:11:22:33:44:55 ARP ({}B)\n", frame.len());

        assert_eq!(output_str, expected);
    }

    #[test]
    fn test_pretty_print_ethernet_frame_full_output() {
        let frame = create_ipv4_tcp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = r#"--- Ethernet Frame 62 Bytes ---
  Source MAC:      aa:bb:cc:dd:ee:ff
  Destination MAC: 00:11:22:33:44:55
  EtherType:       IPv4 (0x0800)
  --- IPv4 Packet ---
    Version:         4
    Header Length:   20 bytes
    Total Length:    60
    TTL:             64
    Source IP:       192.168.1.100
    Destination IP:  192.168.1.200
    Protocol:        TCP (0x06)
    --- TCP Segment ---
      Source Port:      8080
      Destination Port: 80
      Sequence Number:  305419896
      Ack Number:       2271560481
      Data Offset:      20 bytes
      Window Size:      8192
      Checksum:         0x0000
      Flags:            [PSH, ACK]
      Payload Length:   8 bytes
----------------------
"#;

        assert_eq!(output_str, expected, "{} != {}", output_str, expected);
    }

    #[test]
    fn test_pretty_print_ethernet_frame_udp_full_output() {
        let frame = create_ipv4_udp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = r#"--- Ethernet Frame 50 Bytes ---
  Source MAC:      aa:bb:cc:dd:ee:ff
  Destination MAC: 00:11:22:33:44:55
  EtherType:       IPv4 (0x0800)
  --- IPv4 Packet ---
    Version:         4
    Header Length:   20 bytes
    Total Length:    36
    TTL:             64
    Source IP:       10.0.0.1
    Destination IP:  10.0.0.2
    Protocol:        UDP (0x11)
    --- UDP Datagram ---
      Source Port:      53
      Destination Port: 53
      Length:           16
      Checksum:         0x0000
      Payload Length:   8 bytes
----------------------
"#;

        assert_eq!(output_str, expected);
    }

    #[test]
    fn test_pretty_print_ethernet_frame_vlan_full_output() {
        let frame = create_vlan_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = r#"--- Ethernet Frame 46 Bytes ---
  Source MAC:      aa:bb:cc:dd:ee:ff
  Destination MAC: 00:11:22:33:44:55
  VLAN Tag:
    Priority:      1
    Identifier:    100
  EtherType:       ARP (0x0806)
  L3 Protocol:     ARP (Not parsed)
----------------------
"#;

        assert_eq!(output_str, expected, "{} != {}", output_str, expected);
    }

    #[test]
    fn test_pretty_print_ethernet_frame_ipv6_full_output() {
        let frame = create_ipv6_tcp_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = r#"--- Ethernet Frame 84 Bytes ---
  Source MAC:      aa:bb:cc:dd:ee:ff
  Destination MAC: 00:11:22:33:44:55
  EtherType:       IPv6 (0x86dd)
  --- IPv6 Packet ---
    Version:         6
    Payload Length:  32
    Hop Limit:       64
    Source IP:       2001:db8::1
    Destination IP:  2001:db8::2
    Next Header:     TCP (0x06)
    --- TCP Segment ---
      Source Port:      8080
      Destination Port: 443
      Sequence Number:  305419896
      Ack Number:       2271560481
      Data Offset:      20 bytes
      Window Size:      8192
      Checksum:         0x0000
      Flags:            [SYN]
      Payload Length:   10 bytes
----------------------
"#;

        assert_eq!(output_str, expected);
    }

    #[test]
    fn test_pretty_print_ethernet_frame_malformed_full_output() {
        let frame = create_malformed_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = "Error: Malformed Ethernet frame.\n";

        assert_eq!(output_str, expected);
    }

    #[test]
    fn test_print_ethernet_frame_concise_malformed_full_output() {
        let frame = create_malformed_frame();
        let mut output = Vec::new();

        pretty_print_ethernet_frame_concise(&mut output, &frame, frame.len() as u32).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = "Error: Malformed Ethernet frame.\n";

        assert_eq!(output_str, expected);
    }

    #[test]
    fn test_print_metadata_concise() {
        let meta = pdumppb::RecordMeta {
            timestamp: 1234567890,
            packet_len: 1500,
            worker_idx: 1,
            pipeline_idx: 2,
            data_size: 1000,
            rx_device_id: 3,
            tx_device_id: 4,
            is_drops: false,
        };

        let mut output = Vec::new();
        pretty_print_metadata_concise(&mut output, &meta).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        assert_eq!(output_str, "00:00:01.234.567 Q:I W:1 P:2 RX:3 TX:4 ");
    }

    #[test]
    fn test_pretty_print_metadata() {
        let meta = pdumppb::RecordMeta {
            timestamp: 1234567890,
            packet_len: 1500,
            worker_idx: 1,
            pipeline_idx: 2,
            data_size: 1000,
            rx_device_id: 3,
            tx_device_id: 4,
            is_drops: false,
        };

        let mut output = Vec::new();
        pretty_print_metadata(&mut output, &meta).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        let expected = "\
--- Packet Metadata ---
  Timestamp:      00:00:01.234.567
  Packet Length:  1500
  Worker Index:   1
  Pipeline Index: 2
  Data Size:      1000
  RX Device ID:   3
  TX Device ID:   4
  Packet Queue:   INPUT
----------------------
";
        assert_eq!(output_str, expected);
    }

    #[test]
    fn test_pretty_print_metadata_with_drops() {
        let meta = pdumppb::RecordMeta {
            timestamp: 1234567890,
            packet_len: 1500,
            worker_idx: 1,
            pipeline_idx: 2,
            data_size: 1000,
            rx_device_id: 3,
            tx_device_id: 4,
            is_drops: true,
        };

        let mut output = Vec::new();
        pretty_print_metadata(&mut output, &meta).unwrap();

        let output_str = String::from_utf8(output).unwrap();
        assert!(output_str.contains("Packet Queue:   DROPS"));
    }
}
