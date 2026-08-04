use core::fmt::{self, Display, Formatter};
use std::{
    fs::File,
    path::{Path, PathBuf},
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::{
    CompleteEnv,
    engine::{ArgValueCandidates, CompletionCandidate},
};
use forwardpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    forward_service_client::ForwardServiceClient,
};
use netip::{Contiguous, IpNetwork};
use serde::{Deserialize, Serialize, Serializer};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod forwardpb {
    use serde::Serialize;

    tonic::include_proto!("modules.forward.controlplane.forwardpb.v1");
}

/// Forward module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
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
    /// Ruleset file path.
    #[arg(required = true, long = "rules", value_name = "PATH")]
    pub rules: PathBuf,
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

/// Forwarding direction for a rule's action.
///
/// The uppercase spelling is canonical, matching `ForwardMode`'s proto enum
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
struct ForwardRule {
    target: String,
    mode: ModeKind,
    counter: String,
    devices: Vec<String>,
    vlan_ranges: Vec<VlanRange>,
    srcs: Vec<String>,
    dsts: Vec<String>,
}

impl TryFrom<ForwardRule> for forwardpb::Rule {
    type Error = Box<dyn core::error::Error>;

    fn try_from(forward_rule: ForwardRule) -> Result<Self, Self::Error> {
        let mode: i32 = match forward_rule.mode {
            ModeKind::None => forwardpb::ForwardMode::None.into(),
            ModeKind::In => forwardpb::ForwardMode::In.into(),
            ModeKind::Out => forwardpb::ForwardMode::Out.into(),
            ModeKind::Unknown(mode) => return Err(format!("unknown forward mode {mode}").into()),
        };

        Ok(Self {
            action: Some(forwardpb::Action {
                target: forward_rule.target,
                mode,
                counter: forward_rule.counter,
            }),
            devices: forward_rule.devices.into_iter().map(|m| m.into()).collect(),
            vlan_ranges: forward_rule.vlan_ranges.into_iter().map(Into::into).collect(),
            srcs: forward_rule
                .srcs
                .into_iter()
                .map(|n| Contiguous::<IpNetwork>::parse(&n).map(filterpb::pb::IpNet::from))
                .collect::<Result<Vec<_>, _>>()?,
            dsts: forward_rule
                .dsts
                .into_iter()
                .map(|n| Contiguous::<IpNetwork>::parse(&n).map(filterpb::pb::IpNet::from))
                .collect::<Result<Vec<_>, _>>()?,
        })
    }
}

impl TryFrom<forwardpb::Rule> for ForwardRule {
    type Error = Box<dyn core::error::Error>;

    fn try_from(rule: forwardpb::Rule) -> Result<Self, Self::Error> {
        let action = rule.action.ok_or("forward rule is missing its action")?;
        let mode = match forwardpb::ForwardMode::try_from(action.mode) {
            Ok(forwardpb::ForwardMode::None) => ModeKind::None,
            Ok(forwardpb::ForwardMode::In) => ModeKind::In,
            Ok(forwardpb::ForwardMode::Out) => ModeKind::Out,
            Err(_) => ModeKind::Unknown(action.mode),
        };

        Ok(Self {
            target: action.target,
            mode,
            counter: action.counter,
            devices: rule.devices.into_iter().map(|d| d.name).collect(),
            vlan_ranges: rule.vlan_ranges.into_iter().map(VlanRange::from).collect(),
            srcs: rule.srcs.into_iter().map(|n| n.to_string()).collect(),
            dsts: rule.dsts.into_iter().map(|n| n.to_string()).collect(),
        })
    }
}

/// A forward module configuration as read from or written to a rule file.
///
/// Every rule field is always emitted, including an empty sequence as `[]`
/// and an empty counter as an empty string, and none of them is optional on
/// `update` either.
///
/// A network renders from the address and mask actually stored, so
/// re-applying shown output normalises an address that carries host bits
/// outside its mask. A stored IPv6 network's mask may have a hole at the
/// `/64` boundary. Such a network renders in expanded-mask form, and
/// `update` rejects it because it requires a contiguous mask.
///
/// A `counter` left empty on `update` is not stored empty: the server
/// materialises it to `to_<target>` before storing, bounded to whatever
/// length the shared-memory counter registry accepts by cutting it back
/// to the last whole character that still fits, so `show` renders that
/// name instead of an empty string once the rule has gone through
/// `update`, and a non-empty `counter` is used verbatim.
#[derive(Debug, Serialize, Deserialize)]
pub struct ForwardConfig {
    rules: Vec<ForwardRule>,
}

impl TryFrom<ForwardConfig> for Vec<forwardpb::Rule> {
    type Error = Box<dyn core::error::Error>;

    fn try_from(config: ForwardConfig) -> Result<Self, Self::Error> {
        config.rules.into_iter().map(forwardpb::Rule::try_from).collect()
    }
}

impl TryFrom<Vec<forwardpb::Rule>> for ForwardConfig {
    type Error = Box<dyn core::error::Error>;

    fn try_from(rules: Vec<forwardpb::Rule>) -> Result<Self, Self::Error> {
        Ok(Self {
            rules: rules
                .into_iter()
                .map(ForwardRule::try_from)
                .collect::<Result<Vec<_>, _>>()?,
        })
    }
}

impl ForwardConfig {
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
const SERVICE_NAME: &str = "modules.forward.controlplane.forwardpb.v1.ForwardService";

pub struct ForwardService {
    service: Service<ForwardServiceClient<LayeredChannel>>,
}

impl ForwardService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
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

        let config = ForwardConfig::try_from(response.rules)
            .map_err(|e: Box<dyn core::error::Error>| self.service.invalid("show", e.to_string()))?;

        output::data(
            || &config,
            || {
                print!(
                    "{}",
                    serde_yaml::to_string(&config).expect("forward config YAML serialization must not fail")
                );

                if config.rules.is_empty() {
                    output::empty_with_hint(
                        format_args!("No forward rules found for '{}'.", cmd.config_name),
                        format_args!("create one with 'yanet-cli-forward update --name <name> --rules <path>'"),
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
                        format_args!("create one with 'yanet-cli-forward update --name <name> --rules <path>'"),
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

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let config = ForwardConfig::load(&cmd.rules).map_err(|e| self.service.invalid("update", e.to_string()))?;
        let rules: Vec<forwardpb::Rule> = config
            .try_into()
            .map_err(|e: Box<dyn core::error::Error>| self.service.invalid("update", e.to_string()))?;
        let request = UpdateConfigRequest { name: cmd.config.clone(), rules };
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
    let mut service = ForwardService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::List => service.list_configs().await,
    }
}

fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    start();
}

#[tokio::main(flavor = "current_thread")]
async fn start() {
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
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
    use super::*;

    fn ip_net(cidr: &str) -> filterpb::pb::IpNet {
        Contiguous::<IpNetwork>::parse(cidr)
            .expect("valid cidr in test fixture")
            .into()
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
                srcs: vec![ip_net("192.0.2.0/24"), ip_net("2001:db8::/32")],
                dsts: vec![ip_net("203.0.113.0/24"), ip_net("2001:db8:1::/48")],
            },
            forwardpb::Rule {
                action: Some(forwardpb::Action {
                    target: "target-in".to_string(),
                    mode: forwardpb::ForwardMode::In as i32,
                    counter: String::new(),
                }),
                devices: vec![],
                vlan_ranges: vec![],
                srcs: vec![],
                dsts: vec![],
            },
            forwardpb::Rule {
                action: Some(forwardpb::Action {
                    target: "target-out".to_string(),
                    mode: forwardpb::ForwardMode::Out as i32,
                    counter: "counter-out".to_string(),
                }),
                devices: vec![filterpb::pb::Device { name: "eth2".to_string() }],
                vlan_ranges: vec![filterpb::pb::VlanRange { from: 10, to: 20 }],
                srcs: vec![ip_net("10.0.0.0/8")],
                dsts: vec![ip_net("10.1.0.0/16")],
            },
        ]
    }

    #[test]
    fn a_shown_config_round_trips_through_yaml_back_into_the_original_rules() {
        let rules = sample_rules();

        let config = ForwardConfig::try_from(rules.clone()).expect("pb rules must convert into a forward config");
        let yaml = serde_yaml::to_string(&config).expect("forward config must serialize");
        let parsed: ForwardConfig = serde_yaml::from_str(&yaml).expect("forward config must deserialize");
        let reconstructed: Vec<forwardpb::Rule> = parsed.try_into().expect("forward config must convert back");

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
    srcs: []
    dsts: []
  - target: "t2"
    mode: "IN"
    counter: ""
    devices: []
    vlan_ranges: []
    srcs: []
    dsts: []
  - target: "t3"
    mode: "OUT"
    counter: ""
    devices: []
    vlan_ranges: []
    srcs: []
    dsts: []
"#;
        let legacy = r#"
rules:
  - target: "t1"
    mode: "None"
    counter: ""
    devices: []
    vlan_ranges: []
    srcs: []
    dsts: []
  - target: "t2"
    mode: "In"
    counter: ""
    devices: []
    vlan_ranges: []
    srcs: []
    dsts: []
  - target: "t3"
    mode: "Out"
    counter: ""
    devices: []
    vlan_ranges: []
    srcs: []
    dsts: []
"#;

        for yaml in [uppercase, legacy] {
            let config: ForwardConfig = serde_yaml::from_str(yaml).expect("mode spellings must parse");
            assert!(matches!(config.rules[0].mode, ModeKind::None));
            assert!(matches!(config.rules[1].mode, ModeKind::In));
            assert!(matches!(config.rules[2].mode, ModeKind::Out));
        }
    }

    #[test]
    fn an_unrecognised_mode_number_is_shown_but_rejected_by_update() {
        let rule = forwardpb::Rule {
            action: Some(forwardpb::Action {
                target: "t".to_string(),
                mode: 99,
                counter: String::new(),
            }),
            devices: vec![],
            vlan_ranges: vec![],
            srcs: vec![],
            dsts: vec![],
        };

        let config = ForwardConfig::try_from(vec![rule]).expect("an unrecognised mode must not blank the show");
        assert!(serde_yaml::to_string(&config).is_ok());

        let rebuilt: Result<Vec<forwardpb::Rule>, _> = config.try_into();
        assert!(rebuilt.is_err());
    }
}
