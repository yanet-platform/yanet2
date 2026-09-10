use std::path::PathBuf;

use clap::{CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::serde_with;
use forwardpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    forward_service_client::ForwardServiceClient,
};
use serde::{Deserializer, Serializer};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{self, ConnectionArgs, LayeredChannel, Service},
    completion, display,
    errors::Error,
    output, yaml,
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod forwardpb {
    tonic::include_proto!("modules.forward.controlplane.forwardpb.v1");
}

/// Manages forward module configs.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Delete a config.
    Delete(DeleteCmd),
    /// Create or replace a config.
    Update(UpdateCmd),
    /// Show a config.
    Show(ShowCmd),
    /// List configs.
    List,
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::Delete(..) => "delete",
            Self::Update(..) => "update",
            Self::Show(..) => "show",
            Self::List => "list",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// The name of the module config to show.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module config to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the module config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config: String,
    /// Path to the module config file: the update request in YAML.
    ///
    /// The file spells the wire request, exactly what the generic operator
    /// pushes and what `show` prints: rules with an `action`, `devices` as
    /// named objects, family-typed `sources4/6` and `destinations4/6`
    /// networks, a mode by its declared name or number. An undeclared mode
    /// number, which `show` still prints raw, is refused. The `name` may
    /// be omitted, it is then taken from `--name`, and a file naming
    /// another config is refused.
    #[arg(value_name = "PATH")]
    pub file: PathBuf,
}

/// Serializes a forward mode as its declared name, an undeclared number as
/// the number itself, so one rule from a newer module cannot hide the rest
/// of a configuration.
fn serialize_forward_mode<S: Serializer>(mode: &i32, serializer: S) -> Result<S::Ok, S::Error> {
    serde_with::declared_name(mode, serializer, forwardpb::ForwardMode::as_str_name)
}

/// Deserializes a forward mode from its declared name or number, a null
/// as NONE.
///
/// An undeclared value is refused here, because the service would
/// silently coerce it to NONE rather than reject it.
fn deserialize_forward_mode<'de, D: Deserializer<'de>>(deserializer: D) -> Result<i32, D::Error> {
    serde_with::from_declared_name(deserializer, forwardpb::ForwardMode::from_str_name)
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.forward.controlplane.forwardpb.v1.ForwardService";

fn forward_client(channel: LayeredChannel) -> ForwardServiceClient<LayeredChannel> {
    ForwardServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

pub struct ForwardService {
    service: Service<ForwardServiceClient<LayeredChannel>>,
}

impl ForwardService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, forward_client).await?;

        Ok(Self { service })
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        let response = self
            .service
            .unary_with(
                "show",
                request,
                self.service.not_found("show", &format!("config '{}'", cmd.config_name)),
                async |client, request| client.show_config(request).await,
            )
            .await?;

        output::data(
            || &response,
            || {
                // The document is printed even without rules, so a
                // redirected show yields a file update accepts, an
                // undeclared mode number excepted.
                print!(
                    "{}",
                    serde_yaml::to_string(&response).expect("forward config YAML serialization must not fail")
                );

                if response.rules.is_empty() {
                    output::empty_with_hint(
                        format_args!("No forward rules found for '{}'.", cmd.config_name),
                        format_args!("create one with 'yanet-cli-forward update --name <name> <path>'"),
                    );
                }
            },
        );

        Ok(())
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .unary("list", ListConfigsRequest {}, async |client, request| {
                client.list_configs(request).await
            })
            .await?;

        output::data(
            || &response.configs,
            || {
                display::print_names_with_hint(
                    &response.configs,
                    format_args!("No forward configurations found."),
                    format_args!("create one with 'yanet-cli-forward update --name <name> <path>'"),
                )
            },
        );

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config.clone() };
        self.service
            .unary_with(
                "delete",
                request,
                self.service.not_found("delete", &format!("config '{}'", cmd.config)),
                async |client, request| client.delete_config(request).await,
            )
            .await?;

        output::success("delete", format_args!("Deleted config '{}'.", cmd.config));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd, request: UpdateConfigRequest) -> Result<(), Error> {
        self.service
            .unary("update", request, async |client, request| {
                client.update_config(request).await
            })
            .await?;

        output::success("update", format_args!("Updated config '{}'.", cmd.config));

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();

    // The update file is read and bound before the connection, so bad
    // local input fails deterministically with or without a reachable
    // gateway.
    let update = match &cmd.mode {
        ModeCmd::Update(update) => {
            let endpoint = client::resolve_label(&cmd.globals.connection, action)?;
            let mut request: UpdateConfigRequest = yaml::load_document(&update.file)
                .map_err(|err| Error::invalid_argument("update", endpoint.clone(), err.to_string()))?;
            yaml::bind_name(&mut request.name, &update.config)
                .map_err(|err| Error::invalid_argument("update", endpoint, err))?;
            Some(request)
        }
        _ => None,
    };

    let mut service = ForwardService::new(&cmd.globals.connection, action).await?;

    match cmd.mode {
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => {
            let request = update.expect("prepared for the update mode");
            service.update_config(cmd, request).await
        }
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::List => service.list_configs().await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

/// Completion candidates for a `--name` argument: the forward configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, forward_client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

#[cfg(test)]
mod test {
    use commonpb::pb::{IPv4Network, IPv6Network};

    use super::*;

    fn v4_net(net: &str) -> IPv4Network {
        net.parse().expect("valid IPv4 network in test fixture")
    }

    fn v6_net(net: &str) -> IPv6Network {
        net.parse().expect("valid IPv6 network in test fixture")
    }

    fn sample_rules() -> Vec<forwardpb::Rule> {
        vec![
            forwardpb::Rule {
                action: Some(forwardpb::Action {
                    target: "target-none".to_string(),
                    mode: forwardpb::ForwardMode::None as i32,
                    counter: "counter-none".to_string(),
                }),
                devices: vec![
                    filterpb::pb::Device { name: "eth0".to_string() },
                    filterpb::pb::Device { name: "eth1".to_string() },
                ],
                vlan_ranges: vec![
                    filterpb::pb::VlanRange { from: 0, to: 100 },
                    filterpb::pb::VlanRange { from: 200, to: 300 },
                ],
                sources4: vec![v4_net("192.0.2.0/24")],
                // The second mask hole sits exactly at the /64 boundary,
                // which the filter compiler accepts.
                sources6: vec![v6_net("2001:db8::/32"), v6_net("2001:db8::/ffff:ffff:ffff:0:ffff::")],
                destinations4: vec![v4_net("203.0.113.0/24")],
                destinations6: vec![v6_net("2001:db8:1::/48")],
            },
            forwardpb::Rule {
                action: Some(forwardpb::Action {
                    target: "target-out".to_string(),
                    mode: forwardpb::ForwardMode::Out as i32,
                    counter: String::new(),
                }),
                devices: vec![],
                vlan_ranges: vec![],
                sources4: vec![],
                sources6: vec![],
                destinations4: vec![],
                destinations6: vec![],
            },
        ]
    }

    #[test]
    fn test_shown_config_round_trips_into_the_update_request() {
        let shown = forwardpb::ShowConfigResponse {
            name: "forward0".to_string(),
            rules: sample_rules(),
        };

        let yaml = serde_yaml::to_string(&shown).expect("shown config must serialize");
        let parsed: UpdateConfigRequest = serde_yaml::from_str(&yaml).expect("shown config must parse back");

        assert_eq!(shown.name, parsed.name);
        assert_eq!(shown.rules, parsed.rules);
    }

    #[test]
    fn test_file_fields_default_when_omitted() {
        let yaml = r#"
rules:
  - action:
      target: "01:00.0"
      mode: OUT
      counter: recirc
    devices:
      - name: "01:00.0"
    sources4:
      - "0.0.0.0/0"
"#;

        let parsed: UpdateConfigRequest = serde_yaml::from_str(yaml).expect("a sparse file must parse");

        assert_eq!("", parsed.name);
        assert_eq!(
            forwardpb::ForwardMode::Out as i32,
            parsed.rules[0].action.as_ref().expect("action").mode
        );
        assert!(parsed.rules[0].vlan_ranges.is_empty());
        assert!(parsed.rules[0].sources6.is_empty());
    }

    #[test]
    fn test_file_rejects_an_unknown_key() {
        let yaml = "rules:\n  - action:\n      target: t\n    srcs: []\n";

        let parsed: Result<UpdateConfigRequest, _> = serde_yaml::from_str(yaml);

        assert!(
            parsed
                .expect_err("the legacy srcs key must be refused")
                .to_string()
                .contains("srcs")
        );
    }

    #[test]
    fn test_mode_serializes_by_name_and_an_undeclared_number_as_is() {
        let declared = forwardpb::Action {
            target: "t".to_string(),
            mode: forwardpb::ForwardMode::Out as i32,
            counter: String::new(),
        };
        let undeclared = forwardpb::Action { mode: 99, ..declared.clone() };

        assert!(
            serde_yaml::to_string(&declared)
                .expect("must serialize")
                .contains("mode: OUT")
        );
        assert!(
            serde_yaml::to_string(&undeclared)
                .expect("must serialize")
                .contains("mode: 99")
        );
    }

    #[test]
    fn test_mode_parses_a_declared_name_or_number_and_refuses_the_rest() {
        let by_name: forwardpb::Action = serde_yaml::from_str("mode: IN\n").expect("a declared name must parse");
        let by_number: forwardpb::Action = serde_yaml::from_str("mode: 2\n").expect("a declared number must parse");
        let unknown_name: Result<forwardpb::Action, _> = serde_yaml::from_str("mode: BOGUS\n");
        let unknown_number: Result<forwardpb::Action, _> = serde_yaml::from_str("mode: 99\n");

        assert_eq!(forwardpb::ForwardMode::In as i32, by_name.mode);
        assert_eq!(forwardpb::ForwardMode::Out as i32, by_number.mode);
        assert!(
            unknown_name
                .expect_err("an undeclared name must be refused")
                .to_string()
                .contains("BOGUS")
        );
        assert!(
            unknown_number
                .expect_err("an undeclared number must be refused")
                .to_string()
                .contains("99")
        );
    }

    #[test]
    fn test_extern_messages_default_and_refuse_unknown_keys() {
        let sparse: forwardpb::Rule =
            serde_yaml::from_str("vlan_ranges:\n  - {}\n").expect("an empty vlan range must default");
        let unknown: Result<forwardpb::Rule, _> = serde_yaml::from_str("devices:\n  - name: eth0\n    mtu: 9000\n");

        assert_eq!(vec![filterpb::pb::VlanRange { from: 0, to: 0 }], sparse.vlan_ranges);
        assert!(
            unknown
                .expect_err("an unknown device key must be refused")
                .to_string()
                .contains("mtu")
        );
    }

    #[test]
    fn test_unknown_null_valued_keys_are_still_refused() {
        let yaml = "rulez: null\n";
        let path = std::env::temp_dir().join(format!("fwd-nullkey-{}.yaml", std::process::id()));
        std::fs::write(&path, yaml).expect("the fixture must be written");

        let refused = yaml::load_document::<UpdateConfigRequest>(&path)
            .expect_err("a misspelled null-valued key must be refused");
        std::fs::remove_file(&path).ok();

        assert!(refused.to_string().contains("rulez"));
    }

    #[test]
    fn test_null_fields_read_as_zero_values() {
        let yaml = "name: forward0\nrules:\n  - action:\n      target: t\n      mode: OUT\n      counter: c\n    devices: null\n    sources4: null\n";
        let path = std::env::temp_dir().join(format!("fwd-null-{}.yaml", std::process::id()));
        std::fs::write(&path, yaml).expect("the fixture must be written");

        let request = yaml::load_document::<UpdateConfigRequest>(&path).expect("null fields must load");
        std::fs::remove_file(&path).ok();

        assert_eq!("forward0", request.name);
        assert!(request.rules[0].devices.is_empty());
    }
}
