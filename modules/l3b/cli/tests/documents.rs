use core::{convert::Infallible, time::Duration};
use std::{fs, process::Output, sync::Arc};

use http_body_util::{BodyExt, Full};
use hyper::{Request, Response, body::Incoming, server::conn::http2, service::service_fn};
use hyper_util::rt::{TokioExecutor, TokioIo};
use prost::Message;
use tempfile::TempDir;
use tokio::{net::TcpListener, process::Command, sync::Mutex, task::JoinHandle, time::timeout};
// These dependencies belong to the executable exercised by this integration test.
use {clap as _, log as _, serde_yaml as _, tabled as _, unsafe_libyaml as _};

#[allow(clippy::std_instead_of_core, non_snake_case)]
mod l3bpb {
    use serde::Serialize;

    tonic::include_proto!("modules.l3b.controlplane.l3bpb.v1");
}

const SERVICE: &str = "real_servers: []\nsource_filter_rules: []\n";
const MODULE: &str = "destination_filter_rules: []\n";
const SOURCE: &str =
    "real_servers: []\nsource_filter_rules:\n- net4s: []\n  net6s: []\n  port_ranges: [{from: 0, to: 65535}]\n";
const DESTINATION: &str =
    "destination_filter_rules:\n- net4s: []\n  net6s: []\n  proto_ranges: [{proto: tcp}]\n  service: backend\n";

type Call = (String, Vec<u8>);

/// Owns a reachable HTTP/2 endpoint and records every RPC, including its
/// payload.
struct Endpoint {
    address: String,
    calls: Arc<Mutex<Vec<Call>>>,
    task: JoinHandle<()>,
    directory: TempDir,
}

impl Drop for Endpoint {
    fn drop(&mut self) {
        self.task.abort();
    }
}

impl Endpoint {
    async fn new() -> Self {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await.unwrap();
        let address = format!("grpc://{}", listener.local_addr().unwrap());
        let calls = Arc::new(Mutex::new(Vec::new()));
        let captured = calls.clone();
        let task = tokio::spawn(async move {
            loop {
                let (socket, _) = listener.accept().await.unwrap();
                let captured = captured.clone();
                let service = service_fn(move |request: Request<Incoming>| {
                    let captured = captured.clone();
                    async move {
                        let method = request.uri().path().to_owned();
                        let body = request.into_body().collect().await.unwrap().to_bytes();
                        assert!(body.len() >= 5);
                        let length = u32::from_be_bytes(body[1..5].try_into().unwrap()) as usize;
                        assert_eq!(length + 5, body.len());
                        let payload = match body[0] {
                            0 => body[5..].to_vec(),
                            1 => zstd::decode_all(&body[5..]).unwrap(),
                            flag => panic!("unexpected compression flag: {flag}"),
                        };
                        captured.lock().await.push((method, payload));
                        Ok::<_, Infallible>(
                            Response::builder()
                                .header("content-type", "application/grpc")
                                .header("grpc-status", "0")
                                .body(Full::new(hyper::body::Bytes::from_static(&[0; 5])))
                                .unwrap(),
                        )
                    }
                });
                // The CLI may close after connecting without sending an RPC.
                let _ = http2::Builder::new(TokioExecutor::new())
                    .serve_connection(TokioIo::new(socket), service)
                    .await;
            }
        });
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("cli.yaml"), "{}").unwrap();
        Self { address, calls, task, directory }
    }

    /// Runs the public binary with isolated configuration and a bounded
    /// lifetime.
    async fn run(&self, command: &str, document: &str) -> Output {
        let path = self.directory.path().join("document.yaml");
        fs::write(&path, document).unwrap();
        let mut child = Command::new(env!("CARGO_BIN_EXE_yanet-cli-l3b"));
        child
            .env_clear()
            .env("YANET_CONFIG", self.directory.path().join("cli.yaml"))
            .args(["--endpoint", &self.address, "--auth", "none", "--format", "json"])
            .args([command, "--name", "test", "--file"])
            .arg(path)
            .kill_on_drop(true);
        timeout(Duration::from_secs(10), child.output()).await.unwrap().unwrap()
    }

    /// Local invalid-input errors must name the file and never reach a
    /// mutation.
    async fn rejects(&self, command: &str, document: &str) {
        let before = self.calls.lock().await.len();
        let output = self.run(command, document).await;
        let stderr = String::from_utf8(output.stderr).unwrap();
        assert_eq!(Some(1), output.status.code(), "{command}: {document}\n{stderr}");
        let output: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
        let error = &output["error"];
        assert_eq!("invalid_argument", error["kind"], "{error}");
        let message = error["message"].as_str().unwrap();
        assert!(message.contains("document.yaml"), "{message}");
        assert!(
            message.contains("line")
                || message.contains("EOF")
                || message.contains("duplicate field")
                || message.contains("missing field")
                || message.contains("destination_filter_rules:"),
            "{message}"
        );
        assert_eq!(before, self.calls.lock().await.len(), "{command}: {document}");
    }

    /// Returns the only successful mutation's decoded request and checks its
    /// verb.
    async fn accepts<T: Message + Default>(&self, command: &str, method: &str, document: &str) -> T {
        self.calls.lock().await.clear();
        let output = self.run(command, document).await;
        assert!(
            output.status.success(),
            "{document}\n{}\n{}",
            String::from_utf8_lossy(&output.stderr),
            String::from_utf8_lossy(&output.stdout)
        );
        let calls = self.calls.lock().await;
        assert_eq!(1, calls.len());
        assert_eq!(
            format!("/modules.l3b.controlplane.l3bpb.v1.L3bService/{method}"),
            calls[0].0
        );
        T::decode(calls[0].1.as_slice()).unwrap()
    }
}

#[tokio::test]
async fn test_service_commands_reject_unknown_duplicate_and_null_members() {
    let endpoint = Endpoint::new().await;
    let cases = [
        format!("{SERVICE}unknown: null\n"),
        format!("{SERVICE}realServers: []\n"),
        format!("{SERVICE}real_servers: []\n"),
        format!("{SERVICE}\"real\\u005fservers\": []\n"),
        format!("{SERVICE}session_timeouts: {{typo: null}}\n"),
        format!("{SERVICE}session_timeouts: {{tcp: 1, tcp: 2}}\n"),
        format!("{SERVICE}dscp: {{mode: never, value: 0, typo: null}}\n"),
        format!("{SERVICE}dscp: {{mode: never, mode: always, value: 0}}\n"),
        "real_servers: [{destination_address: 192.0.2.1, source_network: 192.0.2.0/24, typo: null}]\nsource_filter_rules: []".into(),
        "real_servers: [{destination_address: 192.0.2.1, destination_address: 192.0.2.2, source_network: 192.0.2.0/24}]\nsource_filter_rules: []".into(),
        SOURCE.replace("net4s: []", "net4s: []\n  typo: null"),
        SOURCE.replace("net4s: []", "net4s: []\n  net4s: []"),
        SOURCE.replace("from: 0", "from: 0, typo: null"),
        SOURCE.replace("from: 0", "from: 0, from: 1"),
        SERVICE.replace("real_servers: []", "real_servers: [null]"),
        SERVICE.replace("source_filter_rules: []", "source_filter_rules: [null]"),
        SOURCE.replace("net4s: []", "net4s: [null]"),
        SOURCE.replace("net6s: []", "net6s: [null]"),
        SOURCE.replace("[{from: 0, to: 65535}]", "[null]"),
        format!("{SERVICE}session_timeouts: null\n"),
        "".into(),
        format!("{SERVICE}---\n{SERVICE}"),
    ];
    for command in ["create-service", "update-service"] {
        for document in &cases {
            endpoint.rejects(command, document).await;
        }
    }
}

#[tokio::test]
async fn test_module_command_rejects_unknown_duplicate_null_and_scalar_coercion() {
    let endpoint = Endpoint::new().await;
    let cases = [
        format!("{MODULE}unknown: null"),
        format!("{MODULE}destinationFilterRules: []"),
        format!("{MODULE}{MODULE}"),
        DESTINATION.replace("service: backend", "service: backend\n  typo: null"),
        DESTINATION.replace("service: backend", "service: backend\n  service: other"),
        DESTINATION.replace("proto: tcp", "proto: tcp, typo: null"),
        DESTINATION.replace("proto: tcp", "proto: tcp, proto: udp"),
        "destination_filter_rules: [null]".into(),
        DESTINATION.replace("net4s: []", "net4s: [null]"),
        DESTINATION.replace("net6s: []", "net6s: [null]"),
        DESTINATION.replace("[{proto: tcp}]", "[null]"),
        DESTINATION.replace("backend", "42"),
        DESTINATION.replace("backend", "true"),
        DESTINATION.replace("backend", "null"),
        DESTINATION.replace("backend", "[]"),
    ];
    for document in &cases {
        endpoint.rejects("update-module-config", document).await;
    }
}

#[tokio::test]
async fn test_document_commands_reject_yaml_expansion_and_custom_tags() {
    let endpoint = Endpoint::new().await;
    for (command, base, field) in [
        ("create-service", SERVICE, "real_servers"),
        ("update-service", SERVICE, "real_servers"),
        ("update-module-config", MODULE, "destination_filter_rules"),
    ] {
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
            endpoint.rejects(command, &document).await;
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
        endpoint.rejects("create-service", &document).await;
    }
    endpoint
        .rejects(
            "update-module-config",
            &DESTINATION.replace("backend", "!custom backend"),
        )
        .await;
}

#[tokio::test]
async fn test_document_commands_reject_invalid_integer_forms_and_overflow() {
    let endpoint = Endpoint::new().await;
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
            endpoint
                .rejects("create-service", &format!("{SERVICE}{field}: {scalar}"))
                .await;
        }
        for field in ["tcp", "tcp_syn", "tcp_syn_ack", "tcp_fin", "udp", "other"] {
            endpoint
                .rejects(
                    "update-service",
                    &format!("{SERVICE}session_timeouts: {{{field}: {scalar}}}"),
                )
                .await;
        }
        endpoint
            .rejects(
                "create-service",
                &format!("{SERVICE}dscp: {{mode: always, value: {scalar}}}"),
            )
            .await;
        for field in ["from: 0", "to: 65535"] {
            endpoint
                .rejects(
                    "create-service",
                    &SOURCE.replace(field, &format!("{}: {scalar}", field.split(':').next().unwrap())),
                )
                .await;
        }
        for field in ["subtype_from", "subtype_to"] {
            endpoint
                .rejects(
                    "update-module-config",
                    &DESTINATION.replace("proto: tcp", &format!("proto: tcp, {field}: {scalar}")),
                )
                .await;
        }
        endpoint
            .rejects(
                "update-module-config",
                &DESTINATION.replace("proto: tcp", &format!("proto: {scalar}")),
            )
            .await;
    }
    for field in ["session_index_size", "scheduler_flags", "flags"] {
        endpoint
            .rejects("create-service", &format!("{SERVICE}{field}: 4294967296"))
            .await;
    }
    for field in ["tcp", "tcp_syn", "tcp_syn_ack", "tcp_fin", "udp", "other"] {
        endpoint
            .rejects(
                "create-service",
                &format!("{SERVICE}session_timeouts: {{{field}: 4294967296}}"),
            )
            .await;
    }
    endpoint
        .rejects("create-service", &format!("{SERVICE}dscp: {{mode: never, value: 256}}"))
        .await;
    endpoint
        .rejects("create-service", &SOURCE.replace("from: 0", "from: 65536"))
        .await;
    endpoint
        .rejects("create-service", &SOURCE.replace("to: 65535", "to: 65536"))
        .await;
    for field in ["proto", "subtype_from", "subtype_to"] {
        let range = if field == "proto" {
            "proto: 256".into()
        } else {
            format!("proto: tcp, {field}: 256")
        };
        endpoint
            .rejects("update-module-config", &DESTINATION.replace("proto: tcp", &range))
            .await;
    }
    for scalar in ["0", "255"] {
        endpoint
            .rejects(
                "update-module-config",
                &DESTINATION.replace("proto: tcp", &format!("proto: {scalar}")),
            )
            .await;
    }
}

#[tokio::test]
async fn test_service_commands_preserve_complete_wire_requests_and_defaults() {
    let endpoint = Endpoint::new().await;
    let document = "real_servers:\n- destination_address: 192.0.2.1\n  source_network: 198.51.100.0/255.255.255.0\n- destination_address: '2001:db8::1'\n  source_network: '2001:db8::/64'\nsource_filter_rules:\n- net4s: [192.0.2.1]\n  net6s: ['2001:db8::/64']\n  port_ranges: [{from: 0, to: 65535}]\nsession_index_size: 4294967295\nscheduler_flags: 0\nflags: 4294967295\ndscp: {mode: onlydefault, value: 63}\nsession_timeouts: {tcp: 0, tcp_syn: 1, tcp_syn_ack: 4294967295, tcp_fin: 2, udp: 3, other: 4}\n";
    let expected = l3bpb::VirtualService {
        name: "test".into(),
        source_filter_rules: vec![l3bpb::SourceFilterRule {
            net6s: vec!["2001:db8::/64".parse().unwrap()],
            net4s: vec!["192.0.2.1".parse().unwrap()],
            port_ranges: vec![filterpb::pb::PortRange { from: 0, to: 65535 }],
        }],
        real_servers: vec![
            l3bpb::RealServer {
                destination_address: vec![192, 0, 2, 1],
                source_network: Some("198.51.100.0/24".parse().unwrap()),
            },
            l3bpb::RealServer {
                destination_address: "2001:db8::1".parse::<core::net::Ipv6Addr>().unwrap().octets().to_vec(),
                source_network: Some("2001:db8::/64".parse().unwrap()),
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
            other: 4,
        }),
        dscp_flags: 253,
    };
    let create: l3bpb::CreateServiceRequest = endpoint.accepts("create-service", "CreateService", document).await;
    assert_eq!(Some(expected.clone()), create.service);
    let update: l3bpb::UpdateServiceRequest = endpoint.accepts("update-service", "UpdateService", document).await;
    assert_eq!(Some(expected), update.service);
    for document in [
        SERVICE.to_owned(),
        format!("{SERVICE}dscp: null\nsession_timeouts: {{}}"),
        "{real_servers: [], source_filter_rules: []}".into(),
    ] {
        let request: l3bpb::CreateServiceRequest = endpoint.accepts("create-service", "CreateService", &document).await;
        assert_eq!(
            Some(l3bpb::VirtualService {
                name: "test".into(),
                session_timeouts: Some(l3bpb::SessionTimeouts::default()),
                ..Default::default()
            }),
            request.service
        );
    }
    for value in [0, u8::MAX] {
        let request: l3bpb::CreateServiceRequest = endpoint
            .accepts(
                "create-service",
                "CreateService",
                &format!("{SERVICE}dscp: {{mode: always, value: {value}}}"),
            )
            .await;
        assert_eq!(
            2 | ((u32::from(value) << 2) & 0xfc),
            request.service.unwrap().dscp_flags
        );
    }
    for value in [0, u32::MAX] {
        let document = format!(
            "{SERVICE}session_index_size: {value}\nscheduler_flags: {value}\nflags: {value}\nsession_timeouts: {{tcp: {value}, tcp_syn: {value}, tcp_syn_ack: {value}, tcp_fin: {value}, udp: {value}, other: {value}}}"
        );
        let request: l3bpb::UpdateServiceRequest = endpoint.accepts("update-service", "UpdateService", &document).await;
        assert_eq!(
            Some(l3bpb::VirtualService {
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
            }),
            request.service
        );
    }
}

#[tokio::test]
async fn test_module_command_preserves_protocols_strings_and_native_networks() {
    let endpoint = Endpoint::new().await;
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
            let request: l3bpb::UpdateModuleConfigRequest = endpoint
                .accepts("update-module-config", "UpdateModuleConfig", &document)
                .await;
            assert_eq!(
                Some(l3bpb::ModuleConfig {
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
                }),
                request.config
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
        let document = DESTINATION.replace("backend", scalar);
        let request: l3bpb::UpdateModuleConfigRequest = endpoint
            .accepts("update-module-config", "UpdateModuleConfig", &document)
            .await;
        assert_eq!(expected, request.config.unwrap().destination_filter_rules[0].service);
    }
    let document = DESTINATION
        .replace("net4s: []", "net4s: [192.0.2.1/255.255.255.0]")
        .replace("net6s: []", "net6s: ['2001:db8::/64']");
    let request: l3bpb::UpdateModuleConfigRequest = endpoint
        .accepts("update-module-config", "UpdateModuleConfig", &document)
        .await;
    let rule = &request.config.unwrap().destination_filter_rules[0];
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
        let empty: l3bpb::UpdateModuleConfigRequest = endpoint
            .accepts("update-module-config", "UpdateModuleConfig", &document)
            .await;
        assert_eq!(
            Some(l3bpb::ModuleConfig {
                name: "test".into(),
                destination_filter_rules: vec![]
            }),
            empty.config
        );
    }
}
