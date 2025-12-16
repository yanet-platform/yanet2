use std::net::Ipv6Addr;

/// Parse IPv6 address from string to bytes
pub fn parse_ipv6(s: &str) -> Result<Vec<u8>, Box<dyn std::error::Error>> {
    let addr: Ipv6Addr = s.parse()?;
    Ok(addr.octets().to_vec())
}

/// Parse MAC address from string to bytes
pub fn parse_mac(s: &str) -> Result<Vec<u8>, Box<dyn std::error::Error>> {
    let parts: Vec<&str> = s.split(':').collect();
    if parts.len() != 6 {
        return Err(format!("invalid MAC address format: {}", s).into());
    }

    let mut bytes = Vec::with_capacity(6);
    for part in parts {
        let byte = u8::from_str_radix(part, 16).map_err(|_| format!("invalid MAC address byte: {}", part))?;
        bytes.push(byte);
    }

    Ok(bytes)
}