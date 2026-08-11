use core::error::Error;

/// Messages that gain the ordinary `Serialize`/`Deserialize` derive.
///
/// `IPAddress`, `MACAddress`, and `ContiguousIPNetwork` are deliberately
/// absent: `src/lib.rs` hand-writes their `Serialize`/`Deserialize` impls to
/// go straight to and from a plain string, and prost-build's attribute
/// paths are additive, not subtractive -- a blanket `message_attribute(".",
/// ...)` cannot exclude a single message, so every other message is listed
/// here individually instead.
const SERDE_MESSAGES: &[&str] = &[
    ".common.commonpb.v1.ModuleId",
    ".common.commonpb.v1.FunctionId",
    ".common.commonpb.v1.PipelineId",
    ".common.commonpb.v1.Device",
    ".common.commonpb.v1.DevicePipeline",
    ".common.commonpb.v1.GetMetricsRequest",
    ".common.commonpb.v1.MetricTag",
    ".common.commonpb.v1.GetMetricsResponse",
    ".common.commonpb.v1.Metric",
    ".common.commonpb.v1.Label",
    ".common.commonpb.v1.Histogram",
    ".common.commonpb.v1.Bucket",
    ".common.commonpb.v1.IPRange",
];

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=common/commonpb/v1/target.proto");
    println!("cargo:rerun-if-changed=common/commonpb/v1/metric.proto");
    println!("cargo:rerun-if-changed=common/commonpb/v1/macaddr.proto");
    println!("cargo:rerun-if-changed=common/commonpb/v1/ipaddr.proto");
    println!("cargo:rerun-if-changed=common/commonpb/v1/iprange.proto");
    println!("cargo:rerun-if-changed=common/commonpb/v1/ipnetwork.proto");

    let mut config = tonic_build::configure()
        .build_server(false)
        // Covers `Metric`'s `value` oneof, generated as its own enum --
        // `Metric` cannot derive `Serialize`/`Deserialize` unless that enum
        // does too. No message here excludes an enum, so this stays a
        // blanket path unlike `SERDE_MESSAGES` above. The rename is needed
        // because prost upper-camel-cases the oneof member names, and
        // without it the default tag would emit a Rust identifier rather
        // than the proto field name.
        .enum_attribute(
            ".",
            "#[derive(serde::Serialize, serde::Deserialize)]#[serde(rename_all = \"snake_case\")]",
        );
    for message in SERDE_MESSAGES {
        config = config.message_attribute(message, "#[derive(serde::Serialize, serde::Deserialize)]");
    }
    config.compile_protos(
        &[
            "common/commonpb/v1/target.proto",
            "common/commonpb/v1/metric.proto",
            "common/commonpb/v1/macaddr.proto",
            "common/commonpb/v1/ipaddr.proto",
            "common/commonpb/v1/iprange.proto",
            "common/commonpb/v1/ipnetwork.proto",
        ],
        &["../../.."],
    )?;
    Ok(())
}
