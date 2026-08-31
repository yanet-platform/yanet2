use core::{error::Error, time::Duration};
use std::{
    fs,
    io::{self, Write},
};

use pcap_file::{
    DataLink, Endianness,
    pcap::{PcapHeader, PcapPacket, PcapWriter},
    pcapng::{
        PcapNgWriter,
        blocks::{
            Block,
            enhanced_packet::EnhancedPacketBlock,
            interface_description::{InterfaceDescriptionBlock, InterfaceDescriptionOption, TsResolution},
        },
    },
};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;
use tonic::{Status, codec::Streaming};
use ync::errors::root_cause;

use crate::{args::DumpOutputFormat, pdumppb, printer};

enum PdumpOutput {
    Stdout(io::Stdout),
    File(fs::File),
}

impl PdumpOutput {
    fn new(dst: &str) -> io::Result<PdumpOutput> {
        match dst {
            "-" | "/dev/stdout" => Ok(PdumpOutput::Stdout(io::stdout())),
            _ => Ok(PdumpOutput::File(fs::File::create(dst)?)),
        }
    }
}

impl io::Write for PdumpOutput {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        match self {
            PdumpOutput::Stdout(stdout) => stdout.write(buf),
            PdumpOutput::File(file) => file.write(buf),
        }
    }

    fn flush(&mut self) -> io::Result<()> {
        match self {
            PdumpOutput::Stdout(stdout) => stdout.flush(),
            PdumpOutput::File(file) => file.flush(),
        }
    }
}

pub struct Text {
    inner: PdumpOutput,
    pretty: bool,
    base_ts: Option<u64>,
}

pub struct Pcap {
    inner: PcapWriter<PdumpOutput>,
}

pub struct PcapNg {
    inner: PcapNgWriter<PdumpOutput>,
    interface_id: u32,
}

pub enum PdumpWriter {
    Text(Text),
    Pcap(Pcap),
    PcapNg(PcapNg),
}

impl PdumpWriter {
    pub fn new(fmt: DumpOutputFormat, dst: &str, snaplen: u32) -> Result<Self, Box<dyn Error>> {
        let output = PdumpOutput::new(dst)?;

        let writer = match fmt {
            DumpOutputFormat::Text => PdumpWriter::Text(Text {
                inner: output,
                pretty: false,
                base_ts: None,
            }),
            DumpOutputFormat::Pretty => PdumpWriter::Text(Text {
                inner: output,
                pretty: true,
                base_ts: None,
            }),
            DumpOutputFormat::Pcap => {
                let header = PcapHeader {
                    snaplen,
                    ts_resolution: pcap_file::TsResolution::NanoSecond,
                    endianness: Endianness::Little,
                    ..Default::default()
                };
                let pcap_writer = PcapWriter::with_header(output, header)?;
                PdumpWriter::Pcap(Pcap { inner: pcap_writer })
            }
            DumpOutputFormat::PcapNg => {
                let mut pcapng_writer = PcapNgWriter::with_endianness(output, Endianness::Little)?;

                // Create and write an Interface Description Block
                let interface_block = InterfaceDescriptionBlock {
                    linktype: DataLink::ETHERNET,
                    snaplen,
                    options: vec![InterfaceDescriptionOption::IfTsResol(TsResolution::NANO.to_raw())],
                };

                // Write the interface description block
                pcapng_writer.write_block(&Block::InterfaceDescription(interface_block))?;

                // Interface ID is 0 for the first (and only) interface
                let interface_id = 0u32;

                PdumpWriter::PcapNg(PcapNg { inner: pcapng_writer, interface_id })
            }
        };
        Ok(writer)
    }

    fn write(&mut self, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        match self {
            PdumpWriter::Text(writer) => PdumpWriter::write_text(writer, rec),
            PdumpWriter::Pcap(writer) => PdumpWriter::write_pcap(writer, rec),
            PdumpWriter::PcapNg(writer) => PdumpWriter::write_pcapng(writer, rec),
        }
    }

    fn flush(&mut self) -> Result<(), Box<dyn Error>> {
        match self {
            PdumpWriter::Text(writer) => Ok(writer.inner.flush()?),
            PdumpWriter::Pcap(writer) => Ok(writer.inner.flush()?),
            PdumpWriter::PcapNg(writer) => Ok(writer.inner.get_mut().flush()?),
        }
    }

    fn write_text(writer: &mut Text, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        let mut meta = rec
            .meta
            .ok_or_else(|| -> Box<dyn Error> { "pdump record missing metadata".into() })?;

        let ts = match &writer.base_ts {
            None => {
                // Store the timestamp of the first packet in the writer to establish a
                // baseline.
                writer.base_ts = Some(meta.timestamp);
                0
            }
            // Align timestamps relative to the first packet. Records arrive in
            // per-worker arrival order rather than timestamp order, so a record
            // can predate the baseline -- clamp instead of wrapping.
            Some(v) => meta.timestamp.saturating_sub(*v),
        };
        meta.timestamp = ts;

        if writer.pretty {
            printer::pretty_print_metadata(&mut writer.inner, &meta)?;
            printer::pretty_print_ethernet_frame(&mut writer.inner, &rec.data, meta.packet_len)?;
        } else {
            printer::pretty_print_metadata_concise(&mut writer.inner, &meta)?;
            printer::pretty_print_ethernet_frame_concise(&mut writer.inner, &rec.data, meta.packet_len)?;
        }
        Ok(0)
    }

    fn write_pcap(writer: &mut Pcap, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        let meta = rec
            .meta
            .ok_or_else(|| -> Box<dyn Error> { "pdump record missing metadata".into() })?;
        let ts = Duration::from_nanos(meta.timestamp);
        let packet = PcapPacket::new_owned(ts, meta.packet_len, rec.data);
        Ok(writer.inner.write_packet(&packet)?)
    }

    fn write_pcapng(writer: &mut PcapNg, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        let meta = rec
            .meta
            .ok_or_else(|| -> Box<dyn Error> { "pdump record missing metadata".into() })?;
        let ts = Duration::from_nanos(meta.timestamp);

        let mut packet_block = EnhancedPacketBlock::default();
        packet_block.interface_id = writer.interface_id;
        packet_block.timestamp = ts;
        packet_block.original_len = meta.packet_len;
        packet_block.data = rec.data.into();
        packet_block.set_write_ts_resolution(TsResolution::NANO);

        Ok(writer.inner.write_block(&Block::EnhancedPacket(packet_block))?)
    }
}

/// Failure of the capture's writing side, carried out of the blocking task.
pub type WriteError = Box<dyn Error + Send + Sync>;

/// Writes captured records until a limit, the stream or a failure ends the
/// capture, always flushing. An output that goes away is not a failure.
pub fn pdump_write(
    mut writer: PdumpWriter,
    mut rx: mpsc::Receiver<pdumppb::Record>,
    packet_limit: Option<u64>,
) -> Result<(), WriteError> {
    let mut count = 0;
    let captured = loop {
        if let Some(limit) = packet_limit
            && count >= limit
        {
            log::debug!("stopping writer because the packet capture limit has been reached: {limit}");

            break Ok(());
        }

        let Some(rec) = rx.blocking_recv() else {
            break Ok(());
        };

        if let Err(err) = writer.write(rec) {
            if is_broken_pipe(err.as_ref()) {
                log::debug!("the output is closed, stopping the capture");

                break Ok(());
            }

            break Err(WriteError::from(format!(
                "failed to write record: {}",
                root_cause(err.as_ref())
            )));
        };

        count += 1;
    };

    let flushed = match writer.flush() {
        Ok(()) => Ok(()),
        Err(err) if is_broken_pipe(err.as_ref()) => Ok(()),
        Err(err) => Err(WriteError::from(format!(
            "failed to flush the output: {}",
            root_cause(err.as_ref())
        ))),
    };

    captured.and(flushed)
}

/// Reports whether the output went away, which ends a capture piped into a
/// consumer that stopped reading.
///
/// The pcap writers render every failure as one fixed text, so the cause
/// chain decides.
fn is_broken_pipe(err: &(dyn Error + 'static)) -> bool {
    root_cause(err)
        .downcast_ref::<io::Error>()
        .is_some_and(|err| err.kind() == io::ErrorKind::BrokenPipe)
}

/// Forwards records to the writer until the stream ends or the capture is
/// cancelled. A closed channel is a finished writer, not a failure.
pub async fn pdump_stream_reader(
    mut stream: Streaming<pdumppb::Record>,
    tx: mpsc::Sender<pdumppb::Record>,
    done: CancellationToken,
) -> Result<(), Status> {
    loop {
        tokio::select! {
            biased;
            _ = done.cancelled() => {
                return Ok(());
            }
            message = stream.message() => {
                match message {
                    Err(status) => return Err(status),
                    Ok(None) => return Ok(()),
                    Ok(Some(rec)) => {
                        if let Err(err) = tx.send(rec).await {
                            log::debug!("pdump writer is gone, stopping the reader: {err}");
                            return Ok(());
                        };
                    }
                }
            }
        }
    }
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_pdump_write_reports_an_output_that_cannot_take_records() {
        let writer = PdumpWriter::new(DumpOutputFormat::Text, "/dev/full", 65535).expect("must open");
        let (tx, rx) = mpsc::channel(1);
        tx.try_send(pdumppb::Record {
            meta: Some(pdumppb::RecordMeta::default()),
            data: Vec::new(),
        })
        .expect("must accept the record");
        drop(tx);

        let err = pdump_write(writer, rx, None).expect_err("a full output must fail the capture");

        assert!(err.to_string().starts_with("failed to write record: "), "{err}");
    }

    #[test]
    fn test_broken_pipe_is_recognised_through_a_wrapper() {
        let piped = pcap_file::PcapError::IoError(io::Error::from(io::ErrorKind::BrokenPipe));
        let full = pcap_file::PcapError::IoError(io::Error::from(io::ErrorKind::StorageFull));

        assert!(is_broken_pipe(&piped));
        assert!(!is_broken_pipe(&full));
    }

    /// Verifies that a record the writer cannot encode ends the capture with
    /// an error instead of a log line.
    #[test]
    fn test_pdump_write_reports_a_failed_record() {
        let writer = PdumpWriter::new(DumpOutputFormat::Pcap, "/dev/null", 65535).expect("must open");
        let (tx, rx) = mpsc::channel(1);
        tx.try_send(pdumppb::Record { meta: None, data: Vec::new() })
            .expect("must accept the record");
        drop(tx);

        let err = pdump_write(writer, rx, None).expect_err("a record without metadata must fail the capture");

        assert_eq!("failed to write record: pdump record missing metadata", err.to_string());
    }
}
