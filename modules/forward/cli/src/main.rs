use std::path::{Path, PathBuf};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use forwardpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    forward_service_client::ForwardServiceClient,
};
use serde::{Deserialize, Deserializer, Serializer};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{self, ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod forwardpb {
    use serde::{Deserialize, Serialize};

    tonic::include_proto!("modules.forward.controlplane.forwardpb.v1");
}

/// Forward module.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    Delete(DeleteCmd),
    Update(UpdateCmd),
    Show(ShowCmd),
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
    match forwardpb::ForwardMode::try_from(*mode) {
        Ok(mode) => serializer.serialize_str(mode.as_str_name()),
        Err(_) => serializer.serialize_i32(*mode),
    }
}

/// Deserializes a forward mode from its declared name or number, a null
/// as NONE.
///
/// An undeclared value is refused here, because the service would
/// silently coerce it to NONE rather than reject it.
fn deserialize_forward_mode<'de, D: Deserializer<'de>>(deserializer: D) -> Result<i32, D::Error> {
    #[derive(Deserialize)]
    #[serde(untagged)]
    enum NameOrNumber {
        Number(i32),
        Name(String),
    }

    match Option::<NameOrNumber>::deserialize(deserializer)? {
        None => Ok(forwardpb::ForwardMode::None as i32),
        Some(NameOrNumber::Number(mode)) => forwardpb::ForwardMode::try_from(mode)
            .map(|mode| mode as i32)
            .map_err(|_| serde::de::Error::custom(format!("unknown forward mode {mode}"))),
        Some(NameOrNumber::Name(name)) => forwardpb::ForwardMode::from_str_name(&name)
            .map(|mode| mode as i32)
            .ok_or_else(|| serde::de::Error::custom(format!("unknown forward mode {name:?}"))),
    }
}

/// Deserializes a null as the field's zero value, as the operator's YAML
/// decoder reads it.
fn null_as_default<'de, T, D>(deserializer: D) -> Result<T, D::Error>
where
    T: Default + Deserialize<'de>,
    D: Deserializer<'de>,
{
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}

/// Loads the update request from its YAML file.
///
/// The reading matches the generic operator's: merge keys expand, a null
/// field takes its zero value, an empty document is the zero request and
/// a bare document separator is tolerated.
fn load_request<P>(path: P) -> Result<UpdateConfigRequest, Box<dyn core::error::Error>>
where
    P: AsRef<Path>,
{
    let content = std::fs::read_to_string(path)?;
    let mut documents = Vec::new();
    for document in serde_yaml::Deserializer::from_str(&content) {
        let value = serde_yaml::Value::deserialize(document)?;
        if !value.is_null() {
            documents.push(value);
        }
    }

    let mut value = match documents.pop() {
        None => return Ok(UpdateConfigRequest::default()),
        Some(_) if !documents.is_empty() => {
            return Err("the file holds more than one document".into());
        }
        Some(value) => value,
    };
    value.apply_merge()?;
    Ok(serde_yaml::from_value(value)?)
}

/// Binds the config name into a loaded request, refusing a file that names
/// another config.
fn bind_request_name(request: &mut UpdateConfigRequest, name: &str) -> Result<(), String> {
    if request.name.is_empty() {
        request.name = name.to_string();
        return Ok(());
    }
    if request.name != name {
        return Err(format!(
            "the file names config {:?}, but --name is {:?}",
            request.name, name
        ));
    }
    Ok(())
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.forward.controlplane.forwardpb.v1.ForwardService";

pub struct ForwardService {
    service: Service<ForwardServiceClient<LayeredChannel>>,
}

impl ForwardService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, |channel| {
            ForwardServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();

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
                        format_args!("create one with 'yanet-cli-forward update --name <name> --file <path>'"),
                    );
                }
            },
        );

        Ok(())
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        let response = self
            .service
            .client()
            .list_configs(request)
            .await
            .map_err(self.service.status("list"))?
            .into_inner();

        output::data(
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No forward configurations found."),
                        format_args!("create one with 'yanet-cli-forward update --name <name> --file <path>'"),
                    );
                    return;
                }

                for name in &response.configs {
                    println!("{name}");
                }
            },
        );

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config.clone() };
        self.service
            .client()
            .delete_config(request)
            .await
            .map_err(self.service.status("delete"))?;

        output::success("delete", format_args!("Deleted forward config {}.", cmd.config));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd, request: UpdateConfigRequest) -> Result<(), Error> {
        self.service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated forward config {}.", cmd.config));

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
            let endpoint = client::resolve_label(&cmd.connection, action)?;
            let mut request = load_request(&update.file)
                .map_err(|e| Error::invalid_argument("update", endpoint.clone(), e.to_string()))?;
            bind_request_name(&mut request, &update.config)
                .map_err(|e| Error::invalid_argument("update", endpoint, e))?;
            Some(request)
        }
        _ => None,
    };

    let mut service = ForwardService::new(&cmd.connection, action).await?;

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
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

/// Completion candidates for a `--name` argument: the forward configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            ForwardServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
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
    fn test_empty_and_comment_only_files_are_the_zero_request() {
        for content in ["", "# nothing yet\n"] {
            let path = std::env::temp_dir().join(format!("fwd-empty-{}-{}.yaml", std::process::id(), content.len()));
            std::fs::write(&path, content).expect("the fixture must be written");

            let request = load_request(&path).expect("an empty document must load");
            std::fs::remove_file(&path).ok();

            assert_eq!(UpdateConfigRequest::default(), request);
        }
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

        let refused = load_request(&path).expect_err("a misspelled null-valued key must be refused");
        std::fs::remove_file(&path).ok();

        assert!(refused.to_string().contains("rulez"));
    }

    #[test]
    fn test_null_fields_read_as_zero_values() {
        let yaml = "name: forward0\nrules:\n  - action:\n      target: t\n      mode: OUT\n      counter: c\n    devices: null\n    sources4: null\n";
        let path = std::env::temp_dir().join(format!("fwd-null-{}.yaml", std::process::id()));
        std::fs::write(&path, yaml).expect("the fixture must be written");

        let request = load_request(&path).expect("null fields must load");
        std::fs::remove_file(&path).ok();

        assert_eq!("forward0", request.name);
        assert!(request.rules[0].devices.is_empty());
    }

    #[test]
    fn test_trailing_separator_is_tolerated_but_a_second_document_is_not() {
        let trailing = "name: forward0\n---\n";
        let second = "name: forward0\n---\nname: forward1\n";
        let dir = std::env::temp_dir();
        let trailing_path = dir.join(format!("fwd-sep-{}.yaml", std::process::id()));
        let second_path = dir.join(format!("fwd-two-{}.yaml", std::process::id()));
        std::fs::write(&trailing_path, trailing).expect("the fixture must be written");
        std::fs::write(&second_path, second).expect("the fixture must be written");

        let tolerated = load_request(&trailing_path).expect("a bare separator must be tolerated");
        let refused = load_request(&second_path).expect_err("a second document must be refused");
        std::fs::remove_file(&trailing_path).ok();
        std::fs::remove_file(&second_path).ok();

        assert_eq!("forward0", tolerated.name);
        assert!(refused.to_string().contains("more than one document"));
    }

    #[test]
    fn test_file_rejects_duplicate_keys() {
        let yaml = "rules:\n  - action:\n      target: t\n      mode: OUT\n      mode: NONE\n";

        let parsed: Result<serde_yaml::Value, _> = serde_yaml::from_str(yaml);

        assert!(
            parsed
                .expect_err("a duplicate mapping key must be refused")
                .to_string()
                .contains("duplicate")
        );
    }

    #[test]
    fn test_file_expands_merge_keys() {
        let yaml = r#"
rules:
  - &base
    action:
      target: base
      mode: OUT
      counter: base
    vlan_ranges:
      - from: 0
        to: 100
  - <<: *base
    devices:
      - name: eth0
"#;
        let path = std::env::temp_dir().join(format!("fwd-merge-{}.yaml", std::process::id()));
        std::fs::write(&path, yaml).expect("the fixture must be written");

        let request = load_request(&path).expect("a merged document must load");
        std::fs::remove_file(&path).ok();

        assert_eq!(2, request.rules.len());
        assert_eq!("base", request.rules[1].action.as_ref().expect("merged action").target);
        assert_eq!(
            vec![filterpb::pb::Device { name: "eth0".to_string() }],
            request.rules[1].devices
        );
        assert_eq!(request.rules[0].vlan_ranges, request.rules[1].vlan_ranges);
    }

    #[test]
    fn test_bind_request_name_fills_checks_and_refuses() {
        let mut nameless = UpdateConfigRequest::default();
        bind_request_name(&mut nameless, "forward0").expect("an empty name must bind");
        assert_eq!("forward0", nameless.name);

        let mut matching = UpdateConfigRequest {
            name: "forward0".to_string(),
            ..Default::default()
        };
        bind_request_name(&mut matching, "forward0").expect("a matching name must pass");

        let mut mismatched = UpdateConfigRequest {
            name: "other".to_string(),
            ..Default::default()
        };
        let err = bind_request_name(&mut mismatched, "forward0").expect_err("a mismatch must be refused");
        assert!(err.contains("other"));
        assert!(err.contains("forward0"));
    }
}
