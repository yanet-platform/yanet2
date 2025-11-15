use std::error::Error;

use tonic::transport::Channel;

use crate::{
    cfg,
    cmd::{
        ConfigInfoCmd, DisableRealCmd, EnableBalancingCmd, EnableRealCmd, FlushRealUpdatesCmd, InfoMode, Mode,
        RealMode, ShowConfigCmd, StateInfoCmd,
    },
    rpc::{BalancerServiceClient, balancerpb, commonpb},
};

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
        let config = cfg::BalancerConfig::from_file(cmd.services_path.as_str())?.try_into()?;
        self.client
            .enable_balancing(balancerpb::EnableBalancingRequest {
                target: Some(commonpb::TargetModule {
                    config_name: cmd.config_name,
                    dataplane_instance: cmd.instance,
                }),
                sessions_timeouts: Some(balancerpb::SessionsTimeouts {
                    tcp_syn_ack: 60,
                    tcp_syn: 60,
                    tcp_fin: 60,
                    tcp: 60,
                    udp: 60,
                    default: 60,
                }),
                config: Some(config),
                session_table_size: cmd.sessions_table_reserve,
            })
            .await?;
        log::info!("Successfully enabled balancing");
        Ok(())
    }

    async fn enable_real(&mut self, cmd: EnableRealCmd) -> Result<(), Box<dyn Error>> {
        let request: balancerpb::UpdateRealsRequest = cmd.try_into()?;
        self.client.update_reals(request).await?;
        log::info!("Successfully buffered enable request");
        Ok(())
    }

    async fn disable_real(&mut self, cmd: DisableRealCmd) -> Result<(), Box<dyn Error>> {
        let request: balancerpb::UpdateRealsRequest = cmd.try_into()?;
        self.client.update_reals(request).await?;
        log::info!("Successfully buffered disable request");
        Ok(())
    }

    async fn flush_real_updates(&mut self, cmd: FlushRealUpdatesCmd) -> Result<(), Box<dyn Error>> {
        let request: balancerpb::FlushRealUpdatesRequest = cmd.into();
        let response = self.client.flush_real_updates(request).await?.into_inner();
        log::info!("Successfully flushed {} update requests", response.updates_flushed);
        Ok(())
    }

    async fn display_state_info(&mut self, cmd: StateInfoCmd) -> Result<(), Box<dyn Error>> {
        let request = balancerpb::StateInfoRequest {
            target: Some(commonpb::TargetModule {
                config_name: cmd.config_name,
                dataplane_instance: cmd.instance,
            }),
        };
        let result = self.client.state_info(request).await?.into_inner();
        // todo: pretty print
        println!("{:?}", result);
        Ok(())
    }

    async fn display_config_info(&mut self, cmd: ConfigInfoCmd) -> Result<(), Box<dyn Error>> {
        let request = balancerpb::ConfigInfoRequest {
            dataplane_instance: cmd.instance,
            config: cmd.config_name,
            pipeline: cmd.pipeline.unwrap_or_default(),
            function: cmd.function.unwrap_or_default(),
            chain: cmd.chain.unwrap_or_default(),
            device: cmd.device.unwrap_or_default(),
        };
        let result = self.client.config_info(request).await?.into_inner();
        // todo: pretty print
        println!("{:?}", result);
        Ok(())
    }

    pub async fn handle_cmd(&mut self, mode: Mode) -> Result<(), Box<dyn Error>> {
        log::trace!("{mode:?}");
        match mode {
            Mode::Enable(cmd) => self.enable(cmd).await,
            Mode::ShowConfig(cmd) => self.show_config(cmd).await,
            Mode::Real(cmd) => match cmd.mode {
                RealMode::Enable(cmd) => self.enable_real(cmd).await,
                RealMode::Disable(cmd) => self.disable_real(cmd).await,
                RealMode::Flush(cmd) => self.flush_real_updates(cmd).await,
            },
            Mode::Info(cmd) => match cmd.mode {
                InfoMode::State(cmd) => self.display_state_info(cmd).await,
                InfoMode::Config(cmd) => self.display_config_info(cmd).await,
            },
        }
    }
}
