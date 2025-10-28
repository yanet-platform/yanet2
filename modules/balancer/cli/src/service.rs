use std::error::Error;

use tonic::transport::Channel;

use crate::{cfg, cmd::{EnableBalancingCmd, Mode, ShowConfigCmd}, rpc::{balancerpb, commonpb, BalancerServiceClient}};

////////////////////////////////////////////////////////////////////////////////

pub struct BalancerService {
    client: BalancerServiceClient<Channel>,
}

impl BalancerService {
    pub async fn connect(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = BalancerServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    #[allow(unused)]
    async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        unimplemented!("todo")
    }

    async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Box<dyn Error>> {
        let request = balancerpb::ShowConfigRequest {
            target: Some(commonpb::TargetModule {
                config_name: cmd.config_name,
                dataplane_instance: cmd.instance,
            }),
        };
        let response = self.client.show_config(request).await?.into_inner();
        let config = cfg::BalancerConfig::try_from(response.config.ok_or("no config returned")?)?;
    
        println!("{}", serde_yaml::to_string(&config)?);

        Ok(())
    }

    async fn enable(&mut self, cmd: EnableBalancingCmd) -> Result<(), Box<dyn Error>> {
        let config = cfg::BalancerConfig::from_file(&cmd.services_path.as_str())?.into();
        self.client
            .enable_balancing(balancerpb::EnableBalancingRequest {
                target: Some(commonpb::TargetModule {
                    config_name: cmd.config_name,
                    dataplane_instance: cmd.instance,
                }),
                config: Some(config),
                session_table_size: cmd.sessions_table_reserve,
            })
            .await?;
        log::info!("Successfully enabled balancing");
        Ok(())
    }

    pub async fn handle_cmd(&mut self, mode: Mode) -> Result<(), Box<dyn Error>> {
        log::trace!("{mode:?}");
        match mode {
            Mode::Enable(cmd) => self.enable(cmd).await,
            Mode::ShowConfig(cmd) => self.show_config(cmd).await,
        }
    }
}