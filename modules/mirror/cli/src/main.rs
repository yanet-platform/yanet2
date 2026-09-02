use core::fmt::{self, Display, Formatter};
use std::{
    fs::File,
    path::{Path, PathBuf},
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::{IPv4Network, IPv6Network};
use mirrorpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    mirror_service_client::MirrorServiceClient,
};
use serde::{Deserialize, Serialize, Serializer};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod mirrorpb {
    use serde::Serialize;

    tonic::include_proto!("modules.mirror.controlplane.mirrorpb.v1");
}

/// Mirror module.
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
    /// Path to the ruleset YAML file.
    #[arg(value_name = "PATH")]
    pub file: PathBuf,
}

#[derive(Debug, Serialize, Deserialize)]
struct VlanRange {
    from: u32,
    to: u32,
}

impl From<VlanRange> for filterpb::pb::VlanRange {
    fn from(r: VlanRange) -> Self {
        Self { from: r.from, to: r.to }
    }
}

impl From<filterpb::pb::VlanRange> for VlanRange {
    fn from(r: filterpb::pb::VlanRange) -> Self {
        Self { from: r.from, to: r.to }
    }
}

/// Mirroring direction for a rule's action.
///
/// The uppercase spelling is canonical, matching `MirrorMode`'s proto enum
/// names. The PascalCase spellings are accepted because the schema used them
/// previously.
#[derive(Debug, Deserialize)]
enum ModeKind {
    #[serde(rename = "NONE", alias = "None")]
    None,
    #[serde(rename = "IN", alias = "In")]
    In,
    #[serde(rename = "OUT", alias = "Out")]
    Out,
    /// An unrecognised proto enum value.
    ///
    /// `show` renders the raw number so one rule from a newer module cannot
    /// hide the rest of a configuration. `update` rejects it.
    #[serde(skip)]
    Unknown(i32),
}

impl Display for ModeKind {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match self {
            Self::None => write!(f, "NONE"),
            Self::In => write!(f, "IN"),
            Self::Out => write!(f, "OUT"),
            Self::Unknown(mode) => write!(f, "{mode}"),
        }
    }
}

impl Serialize for ModeKind {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.collect_str(self)
    }
}

#[derive(Debug, Serialize, Deserialize)]
struct MirrorRule {
    target: String,
    mode: ModeKind,
    counter: String,
    devices: Vec<String>,
    vlan_ranges: Vec<VlanRange>,
    sources4: Vec<IPv4Network>,
    sources6: Vec<IPv6Network>,
    destinations4: Vec<IPv4Network>,
    destinations6: Vec<IPv6Network>,
}

impl TryFrom<MirrorRule> for mirrorpb::Rule {
    type Error = Box<dyn core::error::Error>;

    fn try_from(mirror_rule: MirrorRule) -> Result<Self, Self::Error> {
        let mode: i32 = match mirror_rule.mode {
            ModeKind::None => mirrorpb::MirrorMode::None.into(),
            ModeKind::In => mirrorpb::MirrorMode::In.into(),
            ModeKind::Out => mirrorpb::MirrorMode::Out.into(),
            ModeKind::Unknown(mode) => return Err(format!("unknown mirror mode {mode}").into()),
        };

        Ok(Self {
            action: Some(mirrorpb::Action {
                target: mirror_rule.target,
                mode,
                counter: mirror_rule.counter,
            }),
            devices: mirror_rule.devices.into_iter().map(|m| m.into()).collect(),
            vlan_ranges: mirror_rule.vlan_ranges.into_iter().map(Into::into).collect(),
            sources4: mirror_rule.sources4,
            sources6: mirror_rule.sources6,
            destinations4: mirror_rule.destinations4,
            destinations6: mirror_rule.destinations6,
        })
    }
}

impl TryFrom<mirrorpb::Rule> for MirrorRule {
    type Error = Box<dyn core::error::Error>;

    fn try_from(rule: mirrorpb::Rule) -> Result<Self, Self::Error> {
        let action = rule.action.ok_or("mirror rule is missing its action")?;
        let mode = match mirrorpb::MirrorMode::try_from(action.mode) {
            Ok(mirrorpb::MirrorMode::None) => ModeKind::None,
            Ok(mirrorpb::MirrorMode::In) => ModeKind::In,
            Ok(mirrorpb::MirrorMode::Out) => ModeKind::Out,
            Err(_) => ModeKind::Unknown(action.mode),
        };

        Ok(Self {
            target: action.target,
            mode,
            counter: action.counter,
            devices: rule.devices.into_iter().map(|d| d.name).collect(),
            vlan_ranges: rule.vlan_ranges.into_iter().map(VlanRange::from).collect(),
            sources4: rule.sources4,
            sources6: rule.sources6,
            destinations4: rule.destinations4,
            destinations6: rule.destinations6,
        })
    }
}

#[derive(Debug, Serialize, Deserialize)]
pub struct MirrorConfig {
    rules: Vec<MirrorRule>,
}

impl TryFrom<MirrorConfig> for Vec<mirrorpb::Rule> {
    type Error = Box<dyn core::error::Error>;

    fn try_from(config: MirrorConfig) -> Result<Self, Self::Error> {
        config.rules.into_iter().map(mirrorpb::Rule::try_from).collect()
    }
}

impl TryFrom<Vec<mirrorpb::Rule>> for MirrorConfig {
    type Error = Box<dyn core::error::Error>;

    fn try_from(rules: Vec<mirrorpb::Rule>) -> Result<Self, Self::Error> {
        Ok(Self {
            rules: rules
                .into_iter()
                .map(MirrorRule::try_from)
                .collect::<Result<Vec<_>, _>>()?,
        })
    }
}

impl MirrorConfig {
    pub fn load<P>(path: P) -> Result<Self, Box<dyn core::error::Error>>
    where
        P: AsRef<Path>,
    {
        let file = File::open(path)?;
        let config = serde_yaml::from_reader(file)?;

        Ok(config)
    }
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.mirror.controlplane.mirrorpb.v1.MirrorService";

pub struct MirrorService {
    service: Service<MirrorServiceClient<LayeredChannel>>,
}

impl MirrorService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, |channel| {
            MirrorServiceClient::new(channel)
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

        let config = MirrorConfig::try_from(response.rules)
            .map_err(|e: Box<dyn core::error::Error>| self.service.invalid("show", e.to_string()))?;

        output::data(
            || &config,
            || {
                print!(
                    "{}",
                    serde_yaml::to_string(&config).expect("mirror config YAML serialization must not fail")
                );

                if config.rules.is_empty() {
                    output::empty_with_hint(
                        format_args!("No mirror rules found for '{}'.", cmd.config_name),
                        format_args!("create one with 'yanet-cli-mirror update --name <name> <path>'"),
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
                        format_args!("No mirror configurations found."),
                        format_args!("create one with 'yanet-cli-mirror update --name <name> <path>'"),
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

        output::success("delete", format_args!("Deleted mirror config {}.", cmd.config));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let config = MirrorConfig::load(&cmd.file).map_err(|e| self.service.invalid("update", e.to_string()))?;
        let rules: Vec<mirrorpb::Rule> = config
            .try_into()
            .map_err(|e: Box<dyn core::error::Error>| self.service.invalid("update", e.to_string()))?;
        let request = UpdateConfigRequest { name: cmd.config.clone(), rules };
        self.service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated mirror config {}.", cmd.config));

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = MirrorService::new(&cmd.connection, action).await?;

    match cmd.mode {
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::List => service.list_configs().await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

/// Completion candidates for a `--name` argument: the mirror configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            MirrorServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}

#[cfg(test)]
mod test {
    use super::*;

    fn v4_net(net: &str) -> IPv4Network {
        net.parse().expect("valid IPv4 network in test fixture")
    }

    fn v6_net(net: &str) -> IPv6Network {
        net.parse().expect("valid IPv6 network in test fixture")
    }

    fn sample_rules() -> Vec<mirrorpb::Rule> {
        vec![
            mirrorpb::Rule {
                action: Some(mirrorpb::Action {
                    target: "target-none".to_string(),
                    mode: mirrorpb::MirrorMode::None as i32,
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
                sources6: vec![v6_net("2001:db8::/32")],
                destinations4: vec![v4_net("203.0.113.0/24")],
                destinations6: vec![v6_net("2001:db8:1::/48")],
            },
            mirrorpb::Rule {
                action: Some(mirrorpb::Action {
                    target: "target-in".to_string(),
                    mode: mirrorpb::MirrorMode::In as i32,
                    counter: String::new(),
                }),
                devices: vec![],
                vlan_ranges: vec![],
                sources4: vec![],
                sources6: vec![],
                destinations4: vec![],
                destinations6: vec![],
            },
            mirrorpb::Rule {
                action: Some(mirrorpb::Action {
                    target: "target-out".to_string(),
                    mode: mirrorpb::MirrorMode::Out as i32,
                    counter: "counter-out".to_string(),
                }),
                devices: vec![filterpb::pb::Device { name: "eth2".to_string() }],
                vlan_ranges: vec![filterpb::pb::VlanRange { from: 10, to: 20 }],
                sources4: vec![v4_net("10.0.0.0/8")],
                sources6: vec![],
                destinations4: vec![v4_net("10.1.0.0/16")],
                destinations6: vec![],
            },
        ]
    }

    #[test]
    fn a_shown_config_round_trips_through_yaml_back_into_the_original_rules() {
        let rules = sample_rules();

        let config = MirrorConfig::try_from(rules.clone()).expect("pb rules must convert into a mirror config");
        let yaml = serde_yaml::to_string(&config).expect("mirror config must serialize");
        let parsed: MirrorConfig = serde_yaml::from_str(&yaml).expect("mirror config must deserialize");
        let reconstructed: Vec<mirrorpb::Rule> = parsed.try_into().expect("mirror config must convert back");

        assert_eq!(rules, reconstructed);
    }

    #[test]
    fn a_rule_file_accepts_the_full_mode_vocabulary() {
        let uppercase = r#"
rules:
  - target: "t1"
    mode: "NONE"
    counter: ""
    devices: []
    vlan_ranges: []
    sources4: []
    sources6: []
    destinations4: []
    destinations6: []
  - target: "t2"
    mode: "IN"
    counter: ""
    devices: []
    vlan_ranges: []
    sources4: []
    sources6: []
    destinations4: []
    destinations6: []
  - target: "t3"
    mode: "OUT"
    counter: ""
    devices: []
    vlan_ranges: []
    sources4: []
    sources6: []
    destinations4: []
    destinations6: []
"#;
        let legacy = r#"
rules:
  - target: "t1"
    mode: "None"
    counter: ""
    devices: []
    vlan_ranges: []
    sources4: []
    sources6: []
    destinations4: []
    destinations6: []
  - target: "t2"
    mode: "In"
    counter: ""
    devices: []
    vlan_ranges: []
    sources4: []
    sources6: []
    destinations4: []
    destinations6: []
  - target: "t3"
    mode: "Out"
    counter: ""
    devices: []
    vlan_ranges: []
    sources4: []
    sources6: []
    destinations4: []
    destinations6: []
"#;

        for yaml in [uppercase, legacy] {
            let config: MirrorConfig = serde_yaml::from_str(yaml).expect("mode spellings must parse");
            assert!(matches!(config.rules[0].mode, ModeKind::None));
            assert!(matches!(config.rules[1].mode, ModeKind::In));
            assert!(matches!(config.rules[2].mode, ModeKind::Out));
        }
    }

    #[test]
    fn a_bi_contiguous_v6_mask_round_trips_through_the_rule_file() {
        let rule = mirrorpb::Rule {
            action: Some(mirrorpb::Action {
                target: "t".to_string(),
                mode: mirrorpb::MirrorMode::None as i32,
                counter: "c".to_string(),
            }),
            devices: vec![],
            vlan_ranges: vec![],
            sources4: vec![],
            // The mask hole sits exactly at the /64 boundary, which the
            // filter compiler accepts.
            sources6: vec![v6_net("2001:db8::/ffff:ffff:ffff:0:ffff::")],
            destinations4: vec![],
            destinations6: vec![],
        };

        let config = MirrorConfig::try_from(vec![rule.clone()]).expect("a bi-contiguous v6 network must render");
        let yaml = serde_yaml::to_string(&config).expect("mirror config must serialize");
        let parsed: MirrorConfig = serde_yaml::from_str(&yaml).expect("mirror config must deserialize");
        let rebuilt: Vec<mirrorpb::Rule> = parsed.try_into().expect("mirror config must convert back");

        assert_eq!(vec![rule], rebuilt);
    }

    #[test]
    fn an_unrecognised_mode_number_is_shown_but_rejected_by_update() {
        let rule = mirrorpb::Rule {
            action: Some(mirrorpb::Action {
                target: "t".to_string(),
                mode: 99,
                counter: String::new(),
            }),
            devices: vec![],
            vlan_ranges: vec![],
            sources4: vec![],
            sources6: vec![],
            destinations4: vec![],
            destinations6: vec![],
        };

        let config = MirrorConfig::try_from(vec![rule]).expect("an unrecognised mode must not blank the show");
        assert!(serde_yaml::to_string(&config).is_ok());

        let rebuilt: Result<Vec<mirrorpb::Rule>, _> = config.try_into();
        assert!(rebuilt.is_err());
    }
}
