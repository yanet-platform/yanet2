use std::{error::Error, net::IpAddr, str::FromStr};

use serde::{Deserialize, Serialize};

use crate::rpc::aclpb;

//// ==== RANGE ==== /////

#[derive(Debug, Serialize, Deserialize)]
pub struct Range {
    pub from: u16,
    pub to: u16,
}

impl From<Range> for aclpb::PortRange {
    fn from(r: Range) -> Self {
        Self {
            from: r.from as u32,
            to: r.to as u32,
        }
    }
}

impl From<Range> for aclpb::ProtoRange {
    fn from(r: Range) -> Self {
        Self {
            from: r.from as u32,
            to: r.to as u32,
        }
    }
}

//// ==== IP NET ==== ////

#[derive(Debug, Serialize, Deserialize)]
pub struct IpNet {
    pub ip: String,
    pub prefix: u32,
}

impl From<IpNet> for aclpb::IpNet {
    fn from(value: IpNet) -> Self {
        let ip = IpAddr::from_str(&value.ip).unwrap();
        Self {
            ip: match ip {
                IpAddr::V4(ipv4) => ipv4.octets().to_vec(),
                IpAddr::V6(ipv6) => ipv6.octets().to_vec(),
            },
            prefix_len: value.prefix,
        }
    }
}

//// ==== ACTION KIND ==== ////

#[derive(Debug, PartialEq, Serialize, Deserialize)]
pub enum ActionKind {
    Pass = 1,
    Deny = 2,
    Count = 3,
    CheckState = 4,
}

impl From<ActionKind> for i32 {
    fn from(a: ActionKind) -> Self {
        a as i32
    }
}

//// ==== FILTER ==== ////

#[derive(Debug, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct Filter {
    pub src6s: Vec<IpNet>,
    pub dst6s: Vec<IpNet>,
    pub src4s: Vec<IpNet>,
    pub dst4s: Vec<IpNet>,
    pub src_port_ranges: Vec<Range>,
    pub dst_port_ranges: Vec<Range>,
    pub devices: Vec<String>,
    pub proto_ranges: Vec<Range>,
    pub keep_state: bool,
}

impl From<Filter> for aclpb::Filter {
    fn from(f: Filter) -> Self {
        Self {
            src6s: f.src6s.into_iter().map(Into::into).collect(),
            dst6s: f.dst6s.into_iter().map(Into::into).collect(),
            src4s: f.src4s.into_iter().map(Into::into).collect(),
            dst4s: f.dst4s.into_iter().map(Into::into).collect(),
            src_port_ranges: f.src_port_ranges.into_iter().map(Into::into).collect(),
            dst_port_ranges: f.dst_port_ranges.into_iter().map(Into::into).collect(),
            devices: f.devices,
            proto_ranges: f.proto_ranges.into_iter().map(Into::into).collect(),
            keep_state: f.keep_state,
            hash_id: "".to_string(),
        }
    }
}

//// ==== RULE ==== /////

#[derive(Debug, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct Rule {
    #[serde(rename = "Filter")]
    pub filter: Filter,
    #[serde(rename = "Action")]
    pub action: ActionKind,
    #[serde(rename = "Log")]
    pub log: bool,
    #[serde(rename = "SkipToCount")]
    pub skip_to_count: u32,
}

impl From<Rule> for aclpb::Rule {
    fn from(rule: Rule) -> Self {
        Self {
            filter: Some(rule.filter.into()),
            action: rule.action.into(),
            log: rule.log,
            skip_to_count: rule.skip_to_count,
        }
    }
}

//// ==== ACL CONFIG ==== ////

#[derive(Debug, Serialize, Deserialize)]
pub struct AclConfig {
    #[serde(rename = "Rule")]
    pub rules: Vec<Rule>,
}

impl From<AclConfig> for Vec<aclpb::Rule> {
    fn from(config: AclConfig) -> Self {
        config.rules.into_iter().map(From::from).collect()
    }
}

impl AclConfig {
    pub fn from_file(path: &str) -> Result<Self, Box<dyn Error>> {
        let file = std::fs::File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }
}

//// ==== TESTS ==== ////

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_full_rule_parsing() {
        let yaml = r#"
Rule:
  - Filter:
      Src6s: []
      Dst6s: []
      Src4s:
        - ip: "192.0.2.0"
          prefix: 24
      Dst4s:
        - ip: "192.0.3.1"
          prefix: 32
      SrcPortRanges: []
      DstPortRanges:
        - from: 150
          to: 450
        - from: 600
          to: 600
      Devices: []
      ProtoRanges:
        - from: 17
          to: 17
      KeepState: false
    Action: Pass
    Log: false
    SkipToCount: 0
"#;
        let cfg: AclConfig = serde_yaml::from_str(yaml).unwrap();
        assert_eq!(cfg.rules.len(), 1);

        let rule = &cfg.rules[0];
        let filter = &rule.filter;

        // IPv6
        assert!(filter.src6s.is_empty());
        assert!(filter.dst6s.is_empty());

        // IPv4
        assert_eq!(filter.src4s.len(), 1);
        assert_eq!(filter.src4s[0].ip, "192.0.2.0");
        assert_eq!(filter.src4s[0].prefix, 24);

        assert_eq!(filter.dst4s.len(), 1);
        assert_eq!(filter.dst4s[0].ip, "192.0.3.1");
        assert_eq!(filter.dst4s[0].prefix, 32);

        // Ports
        assert!(filter.src_port_ranges.is_empty());
        assert_eq!(filter.dst_port_ranges.len(), 2);
        assert_eq!(filter.dst_port_ranges[0].from, 150);
        assert_eq!(filter.dst_port_ranges[0].to, 450);
        assert_eq!(filter.dst_port_ranges[1].from, 600);
        assert_eq!(filter.dst_port_ranges[1].to, 600);

        // Devices
        assert!(filter.devices.is_empty());

        // Protocols
        assert_eq!(filter.proto_ranges.len(), 1);
        assert_eq!(filter.proto_ranges[0].from, 17);
        assert_eq!(filter.proto_ranges[0].to, 17);

        assert!(!filter.keep_state);

        assert_eq!(rule.action, ActionKind::Pass);
        assert!(!rule.log);
        assert_eq!(rule.skip_to_count, 0);
    }

    #[test]
    fn test_action_kinds() {
        let yaml = r#"
Rule:
  - Filter:
      Src6s: []
      Dst6s: []
      Src4s:
        - ip: "10.0.0.1"
          prefix: 32
      Dst4s: []
      SrcPortRanges: []
      DstPortRanges: []
      Devices: []
      ProtoRanges: []
      KeepState: false
    Action: Pass
    Log: false
    SkipToCount: 0

  - Filter:
      Src6s: []
      Dst6s: []
      Src4s:
        - ip: "10.0.0.2"
          prefix: 32
      Dst4s: []
      SrcPortRanges: []
      DstPortRanges: []
      Devices: []
      ProtoRanges: []
      KeepState: false
    Action: Deny
    Log: false
    SkipToCount: 0

  - Filter:
      Src6s: []
      Dst6s: []
      Src4s:
        - ip: "10.0.0.3"
          prefix: 32
      Dst4s: []
      SrcPortRanges: []
      DstPortRanges: []
      Devices: []
      ProtoRanges: []
      KeepState: false
    Action: Count
    Log: false
    SkipToCount: 0

  - Filter:
      Src6s: []
      Dst6s: []
      Src4s:
        - ip: "10.0.0.4"
          prefix: 32
      Dst4s: []
      SrcPortRanges: []
      DstPortRanges: []
      Devices: []
      ProtoRanges: []
      KeepState: false
    Action: CheckState
    Log: false
    SkipToCount: 0
"#;
        let cfg: AclConfig = serde_yaml::from_str(yaml).unwrap();
        assert_eq!(cfg.rules.len(), 4);
        assert_eq!(cfg.rules[0].action, ActionKind::Pass);
        assert_eq!(cfg.rules[1].action, ActionKind::Deny);
        assert_eq!(cfg.rules[2].action, ActionKind::Count);
        assert_eq!(cfg.rules[3].action, ActionKind::CheckState);
    }

    #[test]
    fn test_ipv6_rule() {
        let yaml = r#"
Rule:
  - Filter:
      Src6s:
        - ip: "2001:db8::1"
          prefix: 128
      Dst6s:
        - ip: "2001:db8::2"
          prefix: 128
      Src4s: []
      Dst4s: []
      SrcPortRanges: []
      DstPortRanges: []
      Devices: []
      ProtoRanges: []
      KeepState: true
    Action: Pass
    Log: true
    SkipToCount: 0
"#;
        let cfg: AclConfig = serde_yaml::from_str(yaml).unwrap();
        assert_eq!(cfg.rules.len(), 1);

        let rule = &cfg.rules[0];
        let filter = &rule.filter;

        assert_eq!(filter.src6s.len(), 1);
        assert_eq!(filter.src6s[0].ip, "2001:db8::1");
        assert_eq!(filter.src6s[0].prefix, 128);

        assert_eq!(filter.dst6s.len(), 1);
        assert_eq!(filter.dst6s[0].ip, "2001:db8::2");
        assert_eq!(filter.dst6s[0].prefix, 128);

        assert!(filter.keep_state);

        assert_eq!(rule.action, ActionKind::Pass);
        assert!(rule.log);
    }

    #[test]
    fn test_serialization() {
        let config = AclConfig {
            rules: vec![Rule {
                filter: Filter {
                    src6s: vec![],
                    dst6s: vec![],
                    src4s: vec![IpNet {
                        ip: "10.10.10.15".to_string(),
                        prefix: 8,
                    }],
                    dst4s: vec![IpNet {
                        ip: "10.11.12.13".to_string(),
                        prefix: 16,
                    }],
                    src_port_ranges: vec![Range { from: 1, to: 2 }],
                    dst_port_ranges: vec![Range { from: 5, to: 100 }],
                    devices: vec![],
                    proto_ranges: vec![Range { from: 17, to: 17 }],
                    keep_state: false,
                },
                action: ActionKind::Pass,
                log: false,
                skip_to_count: 0,
            }],
        };
        let s = serde_yaml::to_string(&config).unwrap();
        assert!(!s.is_empty());
        println!("{}", s)
    }

    #[test]
    fn test_deserialization() {
        let s = r#"
Rule:
  - Filter:
      Src6s: []
      Dst6s: []
      Src4s:
        - ip: "10.10.0.0"
          prefix: 8
      Dst4s:
        - ip: "10.11.0.0"
          prefix: 16
      SrcPortRanges:
        - from: 1
          to: 2
      DstPortRanges:
        - from: 5
          to: 100
      Devices: []
      ProtoRanges:
        - from: 17
          to: 17
      KeepState: false
    Action: Pass
    Log: false
    SkipToCount: 0
"#;
        let cfg: AclConfig = serde_yaml::from_str(s).unwrap();
        assert_eq!(cfg.rules.len(), 1);
    }
}
