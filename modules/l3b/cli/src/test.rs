use std::fs;

use super::{ModuleConfigCmd, ModuleConfigDocument, ServiceCmd, ServiceDocument, document, l3bpb};

const SERVICE: &str = "real_servers: []\nsource_filter_rules: []\n";
const MODULE: &str = "destination_filter_rules: []\n";
const SOURCE: &str =
    "real_servers: []\nsource_filter_rules:\n- net4s: []\n  net6s: []\n  port_ranges: [{from: 0, to: 65535}]\n";
const DESTINATION: &str =
    "destination_filter_rules:\n- net4s: []\n  net6s: []\n  proto_ranges: [{proto: tcp}]\n  service: backend\n";

#[test]
fn test_document_unknown_fields_warn_preserve_wire_and_reject_in_strict_mode() {
    for (is_module, base, extended, path) in [
        (false, SERVICE.to_owned(), format!("{SERVICE}future: VALUE"), "future"),
        (false, format!("{SERVICE}session_timeouts: {{tcp: 10}}"), format!("{SERVICE}session_timeouts: {{tcp: 10, future: VALUE}}"), "session_timeouts.future"),
        (false, format!("{SERVICE}dscp: {{mode: always, value: 7}}"), format!("{SERVICE}dscp: {{mode: always, value: 7, future: VALUE}}"), "dscp.future"),
        (false, "real_servers: [{destination_address: 192.0.2.1, source_network: 192.0.2.0/24}]\nsource_filter_rules: []".into(), "real_servers: [{destination_address: 192.0.2.1, source_network: 192.0.2.0/24, future: VALUE}]\nsource_filter_rules: []".into(), "real_servers[0].future"),
        (false, SOURCE.into(), SOURCE.replace("net4s: []", "net4s: []\n  future: VALUE"), "source_filter_rules[0].future"),
        (false, SOURCE.into(), SOURCE.replace("from: 0", "from: 0, future: VALUE"), "source_filter_rules[0].port_ranges[0].future"),
        (true, MODULE.into(), format!("{MODULE}future: VALUE"), "future"),
        (true, DESTINATION.into(), DESTINATION.replace("service: backend", "service: backend\n  future: VALUE"), "destination_filter_rules[0].future"),
        (true, DESTINATION.into(), DESTINATION.replace("proto: tcp", "proto: tcp, future: VALUE"), "destination_filter_rules[0].proto_ranges[0].future"),
    ] {
        for value in ["null", "secret-value", "-1", "-0", "+1", "0x10", "1.5", "'1'", "!!int '1'", "18446744073709551616", "{nested: [-1, 0x10, null, {password: secret-value}]}" ] {
            let extended = extended.replace("VALUE", value);
            let directory = tempfile::tempdir().unwrap();
            let file = directory.path().join("document.yaml");
            fs::write(&file, &extended).unwrap();
            let (warnings, error) = if is_module {
                let cmd = ModuleConfigCmd { name: "test".into(), file: Some(file) };
                assert_eq!(module(&base).unwrap(), cmd.load(false).unwrap(), "{path}: {value}");
                (document::parse::<ModuleConfigDocument>(&extended, false).unwrap().1, cmd.load(true).unwrap_err())
            } else {
                let cmd = ServiceCmd { name: "test".into(), hash_mask: 0, index_mask: 0, file: Some(file) };
                assert_eq!(service(&base).unwrap(), cmd.load(false).unwrap(), "{path}: {value}");
                (document::parse::<ServiceDocument>(&extended, false).unwrap().1, cmd.load(true).unwrap_err())
            };
            assert_eq!(vec![format!("ignoring unknown field {path}")], warnings);
            assert!(error.contains(path), "{error}");
            assert!(error.contains("document.yaml"), "{error}");
            assert!(!error.contains("secret-value"), "{error}");
        }
    }
}

#[test]
fn test_document_unknown_subtrees_retain_structural_validation() {
    for extra in [
        "future: null\nfuture: 1",
        "future: null\n\"fut\\u0075re\": 1",
        "future: {nested: [{key: 1, key: 2}]}",
        "future: {nested: [{key: 1, \"k\\u0065y\": 2}]}",
        "future: {1: a, 0x1: b}",
        "future: {1: a, !!int '1': b}",
        "future: {! 1: a, 1: b}",
        "future: {true: a, True: b}",
        "future: {null: a, ~: b}",
        "future: {1.5: a, 1.50: b}",
        "future: {nested: [{<<: {key: 1}}]}",
        "future: {nested: [{!!merge '<<': {key: 1}}]}",
        "future: {'<<': 1, !!str <<: 2}",
        "future: {nested: !custom []}",
        "future: {nested: !custom secret-value}",
        "future: &anchor [*anchor]",
        "future: *missing",
        "future: {nested: [!<tag:example.org,2026:custom> {}]}",
    ] {
        assert!(service(&format!("{SERVICE}{extra}")).is_err(), "{extra}");
        assert!(module(&format!("{MODULE}{extra}")).is_err(), "{extra}");
    }
}

#[test]
fn test_document_unknown_mapping_keys_preserve_yaml_types() {
    for value in [
        "{1: a, '1': b}",
        "{! 1: a, '1': b}",
        "{true: a, 'true': b}",
        "{null: a, 'null': b}",
        "{0x1: a, !!str 0x1: b}",
        "{---: a, null: b}",
    ] {
        let content = format!("{SERVICE}future: {value}");
        assert_eq!(
            vec!["ignoring unknown field future"],
            document::parse::<ServiceDocument>(&content, false).unwrap().1
        );
        assert!(document::parse::<ServiceDocument>(&content, true).is_err());
    }
}

#[test]
fn test_document_string_merge_lookalikes_are_unknown_fields_not_merge_keys() {
    for key in ["'<<'", "\"<<\"", "!!str <<", "! <<"] {
        for nested in [false, true] {
            let extra = if nested {
                format!("future: {{nested: [{{{key}: secret-value}}]}}")
            } else {
                format!("{key}: secret-value")
            };
            for base in [SERVICE, MODULE] {
                let content = format!("{base}{extra}");
                let (warnings, error) = if base == SERVICE {
                    (
                        document::parse::<ServiceDocument>(&content, false).unwrap().1,
                        document::parse::<ServiceDocument>(&content, true).unwrap_err(),
                    )
                } else {
                    (
                        document::parse::<ModuleConfigDocument>(&content, false).unwrap().1,
                        document::parse::<ModuleConfigDocument>(&content, true).unwrap_err(),
                    )
                };
                assert_eq!(1, warnings.len());
                assert!(warnings[0].contains(if nested { "future" } else { "<<" }));
                assert!(!warnings[0].contains("secret-value"));
                assert!(error.starts_with("unknown field"), "{error}");
            }
        }
    }
}

#[test]
fn test_document_unknown_numeric_field_in_second_server_preserves_wire() {
    let base = "real_servers:\n- destination_address: 192.0.2.1\n  source_network: 192.0.2.0/24\n- destination_address: 192.0.2.2\n  source_network: 192.0.2.0/24\nsource_filter_rules: []\n";
    let extended = base.replace("source_filter_rules: []", "  future: 0x10\nsource_filter_rules: []");
    let directory = tempfile::tempdir().unwrap();
    let file = directory.path().join("document.yaml");
    fs::write(&file, &extended).unwrap();
    let cmd = ServiceCmd {
        name: "test".into(),
        hash_mask: 0,
        index_mask: 0,
        file: Some(file),
    };
    assert_eq!(service(base).unwrap(), cmd.load(false).unwrap());
    assert_eq!(
        vec!["ignoring unknown field real_servers[1].future"],
        document::parse::<ServiceDocument>(&extended, false).unwrap().1
    );
    assert!(cmd.load(true).unwrap_err().contains("real_servers[1].future"));
}

#[test]
fn test_document_deep_unknown_subtree_is_ignored_but_still_checked() {
    let prefix = format!("{SERVICE}future: {}", "[".repeat(2048));
    let suffix = "]".repeat(2048);
    let content = format!("{prefix}-1{suffix}");
    assert_eq!(
        vec!["ignoring unknown field future"],
        document::parse::<ServiceDocument>(&content, false).unwrap().1
    );
    assert!(document::parse::<ServiceDocument>(&content, true).is_err());
    for strict in [false, true] {
        assert!(document::parse::<ServiceDocument>(&format!("{prefix}{{key: 1, key: 2}}{suffix}"), strict).is_err());
    }
}

#[test]
fn test_document_unknown_fields_do_not_hide_invalid_known_values() {
    for extra in ["future: {flags: -1}", "'session_timeouts.tcp': 0x10", "'flags': null"] {
        assert!(service(&format!("{SERVICE}{extra}\nsession_timeouts: {{tcp: 0x10}}\n")).is_err());
    }
    assert!(
        module(&format!(
            "{}future: null",
            DESTINATION.replace("proto: tcp", "proto: 0")
        ))
        .is_err()
    );
}

#[test]
fn test_document_warnings_escape_and_bound_paths_without_values() {
    let extended = format!(
        "{SERVICE}\"line\\nkey\\u001b\": secret-value\n'{}': secret-value",
        "x".repeat(512)
    );
    let (_, warnings) = document::parse::<ServiceDocument>(&extended, false).unwrap();
    assert_eq!(2, warnings.len());
    for warning in &warnings {
        assert!(
            warning.is_ascii() && !warning.chars().any(char::is_control),
            "{warning:?}"
        );
        assert!(warning.len() <= 282, "{}", warning.len());
        assert!(!warning.contains("secret-value"));
    }
    assert!(warnings[0].contains("line"));
    assert!(warnings[1].ends_with("..."));
    let extended = format!(
        "{SERVICE}{}",
        (0..20)
            .map(|i| format!("future{i}: secret-value\n"))
            .collect::<String>()
    );
    let (_, warnings) = document::parse::<ServiceDocument>(&extended, false).unwrap();
    assert_eq!(17, warnings.len());
    assert_eq!("4 additional unknown fields ignored", warnings[16]);
    assert!(document::parse::<ServiceDocument>(&extended, true).is_err());
}

/// Loads a service through the same file and conversion path as both commands.
fn service(document: &str) -> Result<l3bpb::VirtualService, String> {
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("document.yaml");
    fs::write(&path, document).unwrap();
    let cmd = ServiceCmd {
        name: "test".into(),
        hash_mask: 0,
        index_mask: 0,
        file: Some(path),
    };
    let result = cmd.load(false);
    let strict = cmd.load(true);
    assert_eq!(result.is_ok(), strict.is_ok());
    if result.is_ok() {
        assert_eq!(result, strict);
    }
    result
}

/// Loads a module through the command's file and conversion path.
fn module(document: &str) -> Result<l3bpb::ModuleConfig, String> {
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("document.yaml");
    fs::write(&path, document).unwrap();
    let cmd = ModuleConfigCmd {
        name: "test".into(),
        file: Some(path),
    };
    let result = cmd.load(false);
    let strict = cmd.load(true);
    assert_eq!(result.is_ok(), strict.is_ok());
    if result.is_ok() {
        assert_eq!(result, strict);
    }
    result
}

#[test]
fn test_service_load_rejects_duplicate_and_null_members() {
    for document in [
        format!("{SERVICE}real_servers: []\n"),
        format!("{SERVICE}\"real\\u005fservers\": []\n"),
        format!("{SERVICE}session_timeouts: {{tcp: 1, tcp: 2}}\n"),
        format!("{SERVICE}dscp: {{mode: never, mode: always, value: 0}}\n"),
        "real_servers: [{destination_address: 192.0.2.1, destination_address: 192.0.2.2, source_network: 192.0.2.0/24}]\nsource_filter_rules: []".into(),
        SOURCE.replace("net4s: []", "net4s: []\n  net4s: []"),
        SOURCE.replace("from: 0", "from: 0, from: 1"),
        SERVICE.replace("real_servers: []", "real_servers: [null]"),
        SERVICE.replace("source_filter_rules: []", "source_filter_rules: [null]"),
        SOURCE.replace("net4s: []", "net4s: [null]"),
        SOURCE.replace("net6s: []", "net6s: [null]"),
        SOURCE.replace("[{from: 0, to: 65535}]", "[null]"),
        format!("{SERVICE}session_timeouts: null\n"),
        "".into(),
        format!("{SERVICE}---\n{SERVICE}"),
    ] {
        assert!(service(&document).is_err(), "{document}");
    }
}

#[test]
fn test_module_load_rejects_duplicate_null_and_scalar_coercion() {
    for document in [
        format!("{MODULE}{MODULE}"),
        DESTINATION.replace("service: backend", "service: backend\n  service: other"),
        DESTINATION.replace("proto: tcp", "proto: tcp, proto: udp"),
        "destination_filter_rules: [null]".into(),
        DESTINATION.replace("net4s: []", "net4s: [null]"),
        DESTINATION.replace("net6s: []", "net6s: [null]"),
        DESTINATION.replace("[{proto: tcp}]", "[null]"),
        DESTINATION.replace("backend", "42"),
        DESTINATION.replace("backend", "true"),
        DESTINATION.replace("backend", "null"),
        DESTINATION.replace("backend", "[]"),
    ] {
        assert!(module(&document).is_err(), "{document}");
    }
}

#[test]
fn test_document_load_rejects_yaml_expansion_tags_and_syntax_errors() {
    for (base, field) in [(SERVICE, "real_servers"), (MODULE, "destination_filter_rules")] {
        for document in [
            format!("{base}<<: {{}}"),
            format!("!custom\n{base}"),
            format!("--- !custom\n{base}"),
            format!("--- !<tag:example.org,2026:custom>\n{base}"),
            format!("!<tag:example.org,2026:custom>\n{base}"),
            format!("%TAG !e! tag:example.org,2026:\n---\n!e!custom\n{base}"),
            base.replace(&format!("{field}: []"), &format!("{field}: !custom []")),
            base.replace(&format!("{field}: []"), &format!("{field}: &loop [*loop]")),
            base.replace(&format!("{field}: []"), &format!("{field}: *missing")),
            format!("{field}: ["),
            format!("{field}: 'unterminated"),
            format!("\t{base}"),
            format!("{base}\0"),
        ] {
            let rejected = if base == SERVICE {
                service(&document).is_err()
            } else {
                module(&document).is_err()
            };
            assert!(rejected, "{document}");
        }
    }
    for document in [
        "real_servers: &empty []\nsource_filter_rules: *empty".to_owned(),
        format!("{SERVICE}flags: !custom 1"),
        format!("{SERVICE}flags: !<tag:example.org,2026:integer> 1"),
        format!("{SERVICE}flags: !!int '1'"),
        format!("{SERVICE}dscp: {{mode: !custom never, value: 0}}"),
        format!("{SERVICE}session_timeouts: !custom {{}}"),
        format!("{SERVICE}flags: !!int |\n  1\n"),
        SOURCE.replace("net4s: []", "net4s: [!custom 192.0.2.0/24]"),
        SOURCE.replace("from: 0", "from: !custom 0"),
        SOURCE.replace("net4s: []", "net4s: []\n  <<: {}"),
        format!("{SERVICE}session_timeouts: {{<<: {{tcp: 1}}}}"),
    ] {
        assert!(service(&document).is_err(), "{document}");
    }
    assert!(module(&DESTINATION.replace("backend", "!custom backend")).is_err());
}

#[test]
fn test_document_load_rejects_invalid_integer_forms_and_overflow() {
    for scalar in [
        "-1",
        "-0",
        "+1",
        "0x10",
        "0o10",
        "0b10",
        "'1'",
        "\"1\"",
        "1.0",
        "1e0",
        "true",
        "[]",
        "{}",
        "!!str 1",
        "!!float 1",
        "!!int '1'",
        "!!int \"1\"",
        "! 0x10",
        "18446744073709551616",
        "01",
    ] {
        for field in ["session_index_size", "scheduler_flags", "flags"] {
            assert!(
                service(&format!("{SERVICE}{field}: {scalar}")).is_err(),
                "{field}: {scalar}"
            );
        }
        for field in ["tcp", "tcp_syn", "tcp_syn_ack", "tcp_fin", "udp", "other"] {
            assert!(
                service(&format!("{SERVICE}session_timeouts: {{{field}: {scalar}}}")).is_err(),
                "{field}: {scalar}"
            );
        }
        assert!(
            service(&format!("{SERVICE}dscp: {{mode: always, value: {scalar}}}")).is_err(),
            "dscp: {scalar}"
        );
        for (field, old) in [("from", "0"), ("to", "65535")] {
            assert!(
                service(&SOURCE.replace(&format!("{field}: {old}"), &format!("{field}: {scalar}"))).is_err(),
                "{field}: {scalar}"
            );
        }
        for field in ["subtype_from", "subtype_to"] {
            assert!(
                module(&DESTINATION.replace("proto: tcp", &format!("proto: tcp, {field}: {scalar}"))).is_err(),
                "{field}: {scalar}"
            );
        }
        assert!(
            module(&DESTINATION.replace("proto: tcp", &format!("proto: {scalar}"))).is_err(),
            "proto: {scalar}"
        );
    }
    for field in ["session_index_size", "scheduler_flags", "flags"] {
        assert!(service(&format!("{SERVICE}{field}: 4294967296")).is_err(), "{field}");
    }
    for field in ["tcp", "tcp_syn", "tcp_syn_ack", "tcp_fin", "udp", "other"] {
        assert!(
            service(&format!("{SERVICE}session_timeouts: {{{field}: 4294967296}}")).is_err(),
            "{field}"
        );
    }
    assert!(service(&format!("{SERVICE}dscp: {{mode: never, value: 256}}")).is_err());
    assert!(service(&SOURCE.replace("from: 0", "from: 65536")).is_err());
    assert!(service(&SOURCE.replace("to: 65535", "to: 65536")).is_err());
    for field in ["proto", "subtype_from", "subtype_to"] {
        let range = if field == "proto" {
            "proto: 256".into()
        } else {
            format!("proto: tcp, {field}: 256")
        };
        assert!(module(&DESTINATION.replace("proto: tcp", &range)).is_err(), "{field}");
    }
    for scalar in ["0", "255"] {
        assert!(
            module(&DESTINATION.replace("proto: tcp", &format!("proto: {scalar}"))).is_err(),
            "{scalar}"
        );
    }
}

#[test]
fn test_service_load_preserves_complete_protobuf_configuration_and_defaults() {
    let document = "real_servers:\n- destination_address: 192.0.2.1\n  source_network: 198.51.100.0/255.255.255.0\n- destination_address: '2001:db8::1'\n  source_network: '2001:db8::/64'\nsource_filter_rules:\n- net4s: [192.0.2.1]\n  net6s: ['2001:db8::/64']\n  port_ranges: [{from: 0, to: 65535}]\nsession_index_size: 4294967295\nscheduler_flags: 0\nflags: 4294967295\ndscp: {mode: onlydefault, value: 63}\nsession_timeouts: {tcp: 0, tcp_syn: 1, tcp_syn_ack: 4294967295, tcp_fin: 2, udp: 3, other: 4}\n";
    assert_eq!(
        l3bpb::VirtualService {
            name: "test".into(),
            source_filter_rules: vec![l3bpb::SourceFilterRule {
                net6s: vec!["2001:db8::/64".parse().unwrap()],
                net4s: vec!["192.0.2.1".parse().unwrap()],
                port_ranges: vec![filterpb::pb::PortRange { from: 0, to: 65535 }],
            }],
            real_servers: vec![
                l3bpb::RealServer {
                    destination_address: vec![192, 0, 2, 1],
                    source_network: Some("198.51.100.0/24".parse().unwrap())
                },
                l3bpb::RealServer {
                    destination_address: "2001:db8::1".parse::<core::net::Ipv6Addr>().unwrap().octets().to_vec(),
                    source_network: Some("2001:db8::/64".parse().unwrap())
                },
            ],
            hash_mask: 0,
            index_mask: 0,
            session_index_size: u32::MAX,
            scheduler_flags: 0,
            flags: u32::MAX,
            session_timeouts: Some(l3bpb::SessionTimeouts {
                tcp: 0,
                tcp_syn: 1,
                tcp_syn_ack: u32::MAX,
                tcp_fin: 2,
                udp: 3,
                other: 4
            }),
            dscp_flags: 253,
        },
        service(document).unwrap()
    );
    for document in [
        SERVICE.to_owned(),
        format!("{SERVICE}dscp: null\nsession_timeouts: {{}}"),
        "{real_servers: [], source_filter_rules: []}".into(),
        format!("{SERVICE}flags: !!int 0\ndscp: !!null null"),
    ] {
        assert_eq!(
            l3bpb::VirtualService {
                name: "test".into(),
                session_timeouts: Some(l3bpb::SessionTimeouts::default()),
                ..Default::default()
            },
            service(&document).unwrap()
        );
    }
    for value in [0, u8::MAX] {
        assert_eq!(
            2 | ((u32::from(value) << 2) & 0xfc),
            service(&format!("{SERVICE}dscp: {{mode: always, value: {value}}}"))
                .unwrap()
                .dscp_flags
        );
    }
    for value in [0, u32::MAX] {
        let document = format!(
            "{SERVICE}session_index_size: {value}\nscheduler_flags: {value}\nflags: {value}\nsession_timeouts: {{tcp: {value}, tcp_syn: {value}, tcp_syn_ack: {value}, tcp_fin: {value}, udp: {value}, other: {value}}}"
        );
        assert_eq!(
            l3bpb::VirtualService {
                name: "test".into(),
                session_index_size: value,
                scheduler_flags: value,
                flags: value,
                session_timeouts: Some(l3bpb::SessionTimeouts {
                    tcp: value,
                    tcp_syn: value,
                    tcp_syn_ack: value,
                    tcp_fin: value,
                    udp: value,
                    other: value
                }),
                ..Default::default()
            },
            service(&document).unwrap()
        );
    }
}

#[test]
fn test_module_load_preserves_protocols_strings_and_native_networks() {
    for (protocol, number) in [
        ("tcp", 6),
        ("udp", 17),
        ("icmp", 1),
        ("icmp6", 58),
        ("6", 6),
        ("17", 17),
        ("1", 1),
        ("58", 58),
    ] {
        for subtypes in [
            "",
            ", subtype_from: 0, subtype_to: 255",
            ", subtype_from: null, subtype_to: null",
        ] {
            let document = DESTINATION.replace("proto: tcp", &format!("proto: {protocol}{subtypes}"));
            assert_eq!(
                l3bpb::ModuleConfig {
                    name: "test".into(),
                    destination_filter_rules: vec![l3bpb::DestinationFilterRule {
                        net6s: vec![],
                        net4s: vec![],
                        proto_ranges: vec![filterpb::pb::ProtoRange {
                            from: number << 8,
                            to: (number << 8) | 255
                        }],
                        service: "backend".into(),
                    }]
                },
                module(&document).unwrap()
            );
        }
    }
    for (scalar, expected) in [
        ("'true'", "true"),
        ("\"42\"", "42"),
        ("!!str 0x10", "0x10"),
        ("'!tag *alias <<: &anchor'", "!tag *alias <<: &anchor"),
        ("+backend", "+backend"),
        ("''", ""),
        ("'null'", "null"),
        ("!!str null", "null"),
        ("!<tag:yaml.org,2002:str> 42", "42"),
        ("|\n    !tag *alias\n", "!tag *alias\n"),
    ] {
        assert_eq!(
            expected,
            module(&DESTINATION.replace("backend", scalar))
                .unwrap()
                .destination_filter_rules[0]
                .service
        );
    }
    let document = DESTINATION
        .replace("net4s: []", "net4s: [192.0.2.1/255.255.255.0]")
        .replace("net6s: []", "net6s: ['2001:db8::/64']");
    let config = module(&document).unwrap();
    let rule = &config.destination_filter_rules[0];
    assert_eq!(
        vec!["192.0.2.1/255.255.255.0".parse::<commonpb::pb::IPv4Network>().unwrap()],
        rule.net4s
    );
    assert_eq!(
        vec!["2001:db8::/64".parse::<commonpb::pb::IPv6Network>().unwrap()],
        rule.net6s
    );
    for document in [
        format!("# !tag &anchor *alias\n{MODULE}"),
        "!!map {destination_filter_rules: !!seq []}".into(),
    ] {
        assert_eq!(
            l3bpb::ModuleConfig {
                name: "test".into(),
                destination_filter_rules: vec![]
            },
            module(&document).unwrap()
        );
    }
}

#[test]
fn test_command_load_preserves_flags_and_absent_document() {
    let cmd = ServiceCmd {
        name: "test".into(),
        hash_mask: 17,
        index_mask: 31,
        file: None,
    };
    assert_eq!(
        l3bpb::VirtualService {
            name: "test".into(),
            hash_mask: 17,
            index_mask: 31,
            ..Default::default()
        },
        cmd.load(false).unwrap()
    );
    assert_eq!(
        l3bpb::ModuleConfig {
            name: "test".into(),
            ..Default::default()
        },
        ModuleConfigCmd { name: "test".into(), file: None }.load(false).unwrap()
    );
}
