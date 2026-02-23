//! Example: inspect command output
//! Run with: cargo run --example inspect [format]
//! Where format is one of: normal, detail, json (default: all formats)

mod common;

use std::{env, error::Error};

use yanet_cli_balancer::output::{self, InspectOutputFormat};

fn main() -> Result<(), Box<dyn Error>> {
    let response = common::create_inspect_example();

    let args: Vec<String> = env::args().collect();
    let format = args.get(1).map(|s| s.as_str());

    match format {
        Some("normal") => {
            output::print_show_inspect(&response, InspectOutputFormat::Normal)?;
        }
        Some("detail") => {
            output::print_show_inspect(&response, InspectOutputFormat::Detail)?;
        }
        Some("json") => {
            output::print_show_inspect(&response, InspectOutputFormat::Json)?;
        }
        Some(other) => {
            eprintln!("Unknown format: {}. Use: normal, detail, or json", other);
            std::process::exit(1);
        }
        None => {
            println!("=== inspect: Normal format ===\n");
            output::print_show_inspect(&response, InspectOutputFormat::Normal)?;

            println!("\n\n=== inspect: Detail format ===\n");
            output::print_show_inspect(&response, InspectOutputFormat::Detail)?;

            println!("\n\n=== inspect: JSON format ===\n");
            output::print_show_inspect(&response, InspectOutputFormat::Json)?;
        }
    }

    Ok(())
}
