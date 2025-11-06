use std::error::Error;

use serde::{Deserialize, Serialize};

use crate::rpc::aclpb;

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
struct Net {
    addr: String,
    prefix: u32,
}

impl From<Net> for aclpb::IpNet {
    fn from(value: Net) -> Self {
        Self {
            ip: value.addr.into(),
            prefix_len: value.prefix,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
struct Range {
    from: u32,
    to: u32,
}

impl From<Range> for aclpb::PortRange {
    fn from(r: Range) -> Self {
        Self { from: r.from, to: r.to }
    }
}

impl From<Range> for aclpb::ProtoRange {
    fn from(r: Range) -> Self {
        Self { from: r.from, to: r.to }
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
enum ActionKind {
    Allow,
    Deny,
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
struct AclRule {
    src_net: Net,
    dst_net: Net,
    src_ports: Range,
    dst_ports: Range,
    protos: Range,
    action: ActionKind,
}

impl From<AclRule> for aclpb::Rule {
    fn from(acl_rule: AclRule) -> Self {
        let action = match acl_rule.action {
            ActionKind::Allow => aclpb::ActionKind::Pass,
            ActionKind::Deny => aclpb::ActionKind::Deny,
        };
        let filter = aclpb::Filter {
            src6s: Vec::new(),
            dst6s: Vec::new(),
            // ipv4 only
            src4s: vec![acl_rule.src_net.into()],
            dst4s: vec![acl_rule.dst_net.into()],
            src_port_ranges: vec![acl_rule.src_ports.into()],
            dst_port_ranges: vec![acl_rule.dst_ports.into()],
            devices: vec!["device1".to_string()], // todo: fixme
            proto_ranges: vec![acl_rule.protos.into()],
            keep_state: false,
            hash_id: "123".to_string(), // todo: fixme
        };
        Self {
            filter: Some(filter),
            action: action.into(),
            skip_to_count: 0,
            log: false,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
pub struct AclConfig {
    rules: Vec<AclRule>,
}

impl From<AclConfig> for Vec<aclpb::Rule> {
    fn from(_value: AclConfig) -> Self {
        todo!()
    }
}

////////////////////////////////////////////////////////////////////////////////

impl AclConfig {
    pub fn from_file(path: &str) -> Result<Self, Box<dyn Error>> {
        let file = std::fs::File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn print_some() {
        let config = AclConfig {
            rules: vec![AclRule {
                src_net: Net {
                    addr: "10.10.10.15".to_string(),
                    prefix: 8,
                },
                dst_net: Net {
                    addr: "10.11.12.13".to_string(),
                    prefix: 16,
                },
                src_ports: Range { from: 1, to: 2 },
                dst_ports: Range { from: 5, to: 100 },
                protos: Range { from: 17, to: 17 },
                action: ActionKind::Allow,
            }],
        };
        let s = serde_yaml::to_string(&config).unwrap();
        assert!(!s.is_empty());
        println!("{}", s)
    }

    #[test]
    fn from_string() {
        let s = r#"rules:
- src_net:
    addr: 10.10.10.15
    prefix: 8
  dst_net:
    addr: 10.11.12.13
    prefix: 16
  src_ports:
    from: 1
    to: 2
  dst_ports:
    from: 5
    to: 100
  protos:
    from: 17
    to: 17
  action: Allow
"#;
        let cfg: AclConfig = serde_yaml::from_str(s).unwrap();
        assert_eq!(cfg.rules.len(), 1);
    }
}
