use core::{
    convert::Infallible,
    future::{Ready, ready},
    task::{Context, Poll},
    time::Duration,
};
use std::{
    fs,
    sync::{Arc, Mutex},
};

use tokio::{process::Command, time::timeout};
use tonic::{
    Status,
    body::Body,
    codegen::{
        Service,
        http::{Request, Response},
    },
    server::NamedService,
    transport::{Server, server::TcpIncoming},
};
// Production dependencies are exercised through the public executable.
use {
    clap as _, commonpb as _, filterpb as _, log as _, prost as _, serde as _, serde_yaml as _, tabled as _,
    unsafe_libyaml as _, ync as _,
};

/// Records RPC paths and returns a recognizable remote rejection.
#[derive(Clone, Default)]
struct Recorder(Arc<Mutex<Vec<String>>>);

impl NamedService for Recorder {
    const NAME: &'static str = "modules.l3b.controlplane.l3bpb.v1.L3bService";
}

impl Service<Request<Body>> for Recorder {
    type Response = Response<Body>;
    type Error = Infallible;
    type Future = Ready<Result<Self::Response, Self::Error>>;

    fn poll_ready(&mut self, _: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: Request<Body>) -> Self::Future {
        self.0.lock().unwrap().push(request.uri().path().to_owned());
        ready(Ok(Status::permission_denied("test endpoint reached").into_http()))
    }
}

/// Verifies local failures never reach an RPC; valid controls prove the
/// recorder works.
#[tokio::test]
async fn test_document_commands_reject_before_mutation_rpc() {
    let incoming = TcpIncoming::bind("127.0.0.1:0".parse().unwrap()).unwrap();
    let endpoint = format!("grpc://{}", incoming.local_addr().unwrap());
    let recorder = Recorder::default();
    let server = Server::builder()
        .add_service(recorder.clone())
        .serve_with_incoming(incoming);
    let directory = tempfile::tempdir().unwrap();
    let config = directory.path().join("cli.yaml");
    let file = directory.path().join("document.yaml");
    fs::write(&config, "{}").unwrap();

    let commands = async {
        for (command, method, valid) in [
            (
                "create-service",
                "CreateService",
                "real_servers: []\nsource_filter_rules: []\n",
            ),
            (
                "update-service",
                "UpdateService",
                "real_servers: []\nsource_filter_rules: []\n",
            ),
            (
                "update-module-config",
                "UpdateModuleConfig",
                "destination_filter_rules: []\n",
            ),
        ] {
            let cases = [
                format!("{valid}unknown: null"),
                format!("{valid}{valid}"),
                format!("!custom\n{valid}"),
                "[unterminated".into(),
                valid.to_owned(),
            ];
            for (index, document) in cases.iter().enumerate() {
                fs::write(&file, document).unwrap();
                recorder.0.lock().unwrap().clear();
                let output = timeout(
                    Duration::from_secs(10),
                    Command::new(env!("CARGO_BIN_EXE_yanet-cli-l3b"))
                        .env_clear()
                        .env("YANET_CONFIG", &config)
                        .args(["--endpoint", &endpoint, "--auth", "none", "--format", "json"])
                        .args([command, "--name", "test", "--file"])
                        .arg(&file)
                        .kill_on_drop(true)
                        .output(),
                )
                .await
                .unwrap()
                .unwrap();
                assert_eq!(
                    Some(1),
                    output.status.code(),
                    "{}",
                    String::from_utf8_lossy(&output.stderr)
                );
                let output: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
                let error = &output["error"];
                if index == cases.len() - 1 {
                    assert_eq!("PermissionDenied", error["code"], "{error}");
                    assert_eq!(
                        vec![format!("/{}/{method}", Recorder::NAME)],
                        *recorder.0.lock().unwrap()
                    );
                } else {
                    assert_eq!("invalid_argument", error["kind"], "{error}");
                    let message = error["message"].as_str().unwrap();
                    assert!(message.contains("document.yaml"), "{message}");
                    assert!(
                        message.contains("line") || message.contains("duplicate field"),
                        "{message}"
                    );
                    assert!(recorder.0.lock().unwrap().is_empty(), "{command}: {document}");
                }
            }
        }
    };
    tokio::select! {
        result = server => panic!("test endpoint stopped: {result:?}"),
        () = commands => {}
    }
}
