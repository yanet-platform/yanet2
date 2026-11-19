use core::{error::Error, time::Duration};
use std::{
    fs,
    io::{self, Write},
    time::Instant,
};

use pcap_file::{
    DataLink, Endianness,
    pcap::{PcapHeader, PcapPacket, PcapTsResolution, PcapWriter},
    pcapng::{
        PcapNgWriter,
        blocks::{
            Block,
            enhanced_packet::EnhancedPacketBlock,
            interface_description::{InterfaceDescriptionBlock, InterfaceDescriptionOption, InterfaceTsResolution},
            interface_statistics::{InterfaceStatisticsBlock, InterfaceStatisticsOption},
            opt_common::CommonOption,
        },
    },
};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;
use tonic::Status;
use ync::{errors::root_cause, rpc::Streaming};

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
    latest_packet_timestamp: Option<Duration>,
}

pub enum PdumpWriter {
    Text(Text),
    Pcap(Pcap),
    PcapNg(PcapNg),
}

/// Header snaplen advertised to PCAP and PCAPNG readers, independent of the
/// configured capture snaplen.
///
/// The producer bounds the captured length of every record to 16 bits, so a
/// record never exceeds it. The PCAP writer refuses an oversized record
/// instead of truncating it; the PCAPNG writer relies on the producer's
/// bound alone.
const HEADER_SNAPLEN: u32 = u16::MAX as u32;

impl PdumpWriter {
    pub fn new(fmt: DumpOutputFormat, dst: &str) -> Result<Self, Box<dyn Error>> {
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
                    snaplen: HEADER_SNAPLEN,
                    ts_resolution: PcapTsResolution::NanoSecond,
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
                    snaplen: HEADER_SNAPLEN,
                    options: vec![InterfaceDescriptionOption::IfTsResol(InterfaceTsResolution::NANO)],
                };

                // Write the interface description block
                pcapng_writer.write_block(&Block::InterfaceDescription(interface_block))?;

                // Interface ID is 0 for the first (and only) interface
                let interface_id = 0u32;

                PdumpWriter::PcapNg(PcapNg {
                    inner: pcapng_writer,
                    interface_id,
                    latest_packet_timestamp: None,
                })
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

    /// Records the capture's total of lost records where the format has a
    /// place for it: a pcapng interface statistics block.
    fn write_lost(&mut self, lost: u64) -> Result<(), Box<dyn Error>> {
        let writer = match self {
            PdumpWriter::PcapNg(writer) if lost > 0 => writer,
            _ => return Ok(()),
        };
        let Some(timestamp) = writer.latest_packet_timestamp else {
            return Ok(());
        };

        // Like dumpcap, which stores its capture-buffer drops as isb_ifdrop,
        // and names the counters' source in the block comment.
        let stats = InterfaceStatisticsBlock {
            interface_id: writer.interface_id,
            timestamp,
            options: vec![
                InterfaceStatisticsOption::Common(CommonOption::Comment(
                    "Records lost in the pdump capture ring".into(),
                )),
                InterfaceStatisticsOption::IsbIfDrop(lost),
            ],
        };
        writer.inner.write_block(&Block::InterfaceStatistics(stats))?;
        Ok(())
    }

    fn write_text(writer: &mut Text, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        let mut meta = rec
            .meta
            .ok_or_else(|| -> Box<dyn Error> { "pdump record missing metadata".into() })?;

        let ts = match &writer.base_ts {
            None => {
                // Store the timestamp of the first packet in the writer to
                // establish a baseline.
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
        let packet = PcapPacket::new(ts, meta.packet_len, rec.data)?;
        Ok(writer.inner.write_packet(&packet)?)
    }

    fn write_pcapng(writer: &mut PcapNg, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        let meta = rec
            .meta
            .ok_or_else(|| -> Box<dyn Error> { "pdump record missing metadata".into() })?;
        let ts = Duration::from_nanos(meta.timestamp);

        let packet_block = EnhancedPacketBlock {
            interface_id: writer.interface_id,
            timestamp: ts,
            original_len: meta.packet_len,
            data: rec.data.into(),
            options: vec![],
        };

        let written = writer.inner.write_block(&Block::EnhancedPacket(packet_block))?;
        writer.latest_packet_timestamp = writer.latest_packet_timestamp.max(Some(ts));
        Ok(written)
    }
}

/// Shortest time between two loss reports on stderr.
const LOSS_REPORT_INTERVAL: Duration = Duration::from_secs(1);

/// Failure of the capture's writing side, carried out of the blocking task.
pub type WriteError = Box<dyn Error + Send + Sync>;

/// Writes captured records until a limit, the stream or a failure ends the
/// capture, always flushing. An output that goes away is not a failure.
///
/// The running and final totals of lost records are reported on the loss
/// sink, never as text in the output: a running total at most once per
/// interval, and the capture's total at its end. A failed report is ignored.
pub fn pdump_write<W>(
    mut writer: PdumpWriter,
    mut rx: mpsc::Receiver<pdumppb::Record>,
    packet_limit: Option<u64>,
    mut losses: W,
) -> Result<(), WriteError>
where
    W: Write,
{
    let mut count = 0;
    let mut lost = 0;
    let mut reported_at: Option<Instant> = None;
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

        if rec.lost_before > 0 {
            lost += rec.lost_before;
            if reported_at.is_none_or(|at| at.elapsed() >= LOSS_REPORT_INTERVAL) {
                let _ = writeln!(losses, "lost {lost} records so far");
                reported_at = Some(Instant::now());
            }
        }

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

    let flushed = match writer.write_lost(lost).and_then(|()| writer.flush()) {
        Ok(()) => Ok(()),
        Err(err) if is_broken_pipe(err.as_ref()) => Ok(()),
        Err(err) => Err(WriteError::from(format!(
            "failed to flush the output: {}",
            root_cause(err.as_ref())
        ))),
    };

    if lost > 0 {
        let _ = writeln!(losses, "lost {lost} records in this capture");
    }

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
    use std::thread;

    use pcap_file::{pcap::PcapReader, pcapng::PcapNgReader};
    use tempfile::NamedTempFile;

    use super::*;

    /// Writes a 60-byte and a 1500-byte record in the given format to a fresh
    /// temporary file and returns the file with the payloads written.
    ///
    /// The file is removed when the returned handle is dropped.
    fn write_two_records(fmt: DumpOutputFormat) -> (NamedTempFile, Vec<Vec<u8>>) {
        let file = NamedTempFile::new().expect("must create a temp file");
        let path = file.path().to_str().expect("temp path must be valid UTF-8");
        let mut writer = PdumpWriter::new(fmt, path).expect("must open");

        let mut payloads = Vec::new();
        for len in [60, 1500] {
            let record = pdumppb::Record {
                meta: Some(pdumppb::RecordMeta {
                    packet_len: len as u32,
                    ..Default::default()
                }),
                data: vec![0xAB; len],
                lost_before: 0,
            };
            payloads.push(record.data.clone());
            writer.write(record).expect("must write the record");
        }
        writer.flush().expect("must flush");
        (file, payloads)
    }

    #[test]
    fn test_pcap_has_fixed_header_snaplen_and_keeps_records_intact() {
        let (file, payloads) = write_two_records(DumpOutputFormat::Pcap);

        let mut reader = PcapReader::new(file.reopen().expect("must reopen")).expect("must parse the pcap header");
        assert_eq!(reader.header().snaplen, HEADER_SNAPLEN);

        let mut packets = Vec::new();
        while let Some(packet) = reader.next_packet() {
            packets.push(packet.expect("must parse the record").data().to_vec());
        }
        assert_eq!(packets, payloads);
    }

    #[test]
    fn test_pcapng_has_fixed_header_snaplen_and_keeps_records_intact() {
        let (file, payloads) = write_two_records(DumpOutputFormat::PcapNg);

        let mut reader = PcapNgReader::new(file.reopen().expect("must reopen")).expect("must parse the pcapng header");
        let mut packets = Vec::new();
        while let Some(block) = reader.next_block() {
            if let (Block::EnhancedPacket(packet), _) = block.expect("must parse block") {
                packets.push(packet.data.into_owned());
            }
        }
        assert_eq!(reader.interfaces()[0].snaplen, HEADER_SNAPLEN);
        assert_eq!(packets, payloads);
    }

    /// Returns an empty record with valid metadata that carries only the
    /// given loss count.
    fn lossy_record(lost_before: u64) -> pdumppb::Record {
        pdumppb::Record {
            meta: Some(pdumppb::RecordMeta::default()),
            data: Vec::new(),
            lost_before,
        }
    }

    /// Writes (timestamp in nanoseconds, loss count) pairs to a fresh capture
    /// and reads back its interface statistics blocks.
    fn pcapng_statistics(records: &[(u64, u64)]) -> Vec<InterfaceStatisticsBlock<'static>> {
        let file = NamedTempFile::new().expect("must create a temp file");
        let path = file.path().to_str().expect("temp path must be valid UTF-8");
        let writer = PdumpWriter::new(DumpOutputFormat::PcapNg, path).expect("must open");
        let (tx, rx) = mpsc::channel(records.len());
        for &(timestamp, lost_before) in records {
            let record = pdumppb::Record {
                meta: Some(pdumppb::RecordMeta { timestamp, ..Default::default() }),
                lost_before,
                ..Default::default()
            };
            tx.try_send(record).expect("must accept the record");
        }
        drop(tx);
        pdump_write(writer, rx, None, io::sink()).expect("must write the records");

        let mut reader = PcapNgReader::new(file.reopen().expect("must reopen")).expect("must parse the pcapng header");
        let mut blocks = Vec::new();
        while let Some(block) = reader.next_block() {
            if let (Block::InterfaceStatistics(stats), _) = block.expect("must parse block") {
                blocks.push(stats.into_owned());
            }
        }
        blocks
    }

    #[test]
    fn test_pcapng_records_total_lost_in_one_statistics_block() {
        let [stats] = pcapng_statistics(&[(0, 3), (0, 0), (0, 5)])
            .try_into()
            .expect("one statistics block");
        let comment = CommonOption::Comment("Records lost in the pdump capture ring".into());
        assert!(stats.options.contains(&InterfaceStatisticsOption::Common(comment)));
        assert!(stats.options.contains(&InterfaceStatisticsOption::IsbIfDrop(8)));
        assert_eq!(2, stats.options.len());
    }

    #[test]
    fn test_pcapng_statistics_use_latest_capture_timestamp_for_out_of_order_records() {
        let [stats] = pcapng_statistics(&[(10, 0), (35, 0), (20, 4)])
            .try_into()
            .expect("one statistics block");
        assert_eq!(Duration::from_nanos(35), stats.timestamp);
    }

    #[test]
    fn test_pcapng_without_losses_has_no_statistics_block() {
        assert!(pcapng_statistics(&[(10, 0), (20, 0)]).is_empty());
    }

    #[test]
    fn test_pdump_write_reports_running_total_once_per_interval_and_final_total() {
        let writer = PdumpWriter::new(DumpOutputFormat::Pcap, "/dev/null").expect("must open");
        let (tx, rx) = mpsc::channel(3);
        for lost_before in [3, 0, 5] {
            tx.try_send(lossy_record(lost_before)).expect("must accept the record");
        }
        drop(tx);
        let mut losses = Vec::new();

        pdump_write(writer, rx, None, &mut losses).expect("must write the records");

        assert_eq!(
            concat!("lost 3 records so far\n", "lost 8 records in this capture\n",),
            String::from_utf8(losses).expect("losses must be UTF-8")
        );
    }

    #[test]
    fn test_pdump_write_reports_running_total_again_after_the_interval() {
        let writer = PdumpWriter::new(DumpOutputFormat::Pcap, "/dev/null").expect("must open");
        let (tx, rx) = mpsc::channel(1);
        let mut losses = Vec::new();

        thread::scope(|scope| {
            let capture = scope.spawn(|| pdump_write(writer, rx, None, &mut losses));
            tx.blocking_send(lossy_record(1)).expect("the writer must run");
            // With a one-slot channel this send returns only after the writer
            // took the first record, so the wait below starts after it.
            tx.blocking_send(lossy_record(0)).expect("the writer must run");
            thread::sleep(2 * LOSS_REPORT_INTERVAL);
            tx.blocking_send(lossy_record(2)).expect("the writer must run");
            drop(tx);
            capture
                .join()
                .expect("the writer must not panic")
                .expect("must write the records");
        });

        assert_eq!(
            concat!(
                "lost 1 records so far\n",
                "lost 3 records so far\n",
                "lost 3 records in this capture\n",
            ),
            String::from_utf8(losses).expect("losses must be UTF-8")
        );
    }

    #[test]
    fn test_pdump_write_without_losses_reports_nothing() {
        let writer = PdumpWriter::new(DumpOutputFormat::Pcap, "/dev/null").expect("must open");
        let (tx, rx) = mpsc::channel(1);
        tx.try_send(lossy_record(0)).expect("must accept the record");
        drop(tx);
        let mut losses = Vec::new();

        pdump_write(writer, rx, None, &mut losses).expect("must write the record");

        assert!(losses.is_empty());
    }

    #[test]
    fn test_pdump_write_reports_an_output_that_cannot_take_records() {
        let writer = PdumpWriter::new(DumpOutputFormat::Text, "/dev/full").expect("must open");
        let (tx, rx) = mpsc::channel(1);
        tx.try_send(pdumppb::Record {
            meta: Some(pdumppb::RecordMeta::default()),
            data: Vec::new(),
            lost_before: 0,
        })
        .expect("must accept the record");
        drop(tx);

        let err = pdump_write(writer, rx, None, io::sink()).expect_err("a full output must fail the capture");

        assert!(err.to_string().starts_with("failed to write record: "), "{err}");
    }

    #[test]
    fn test_broken_pipe_is_recognised_through_a_wrapper() {
        let piped = pcap_file::pcap::PcapWriteError::Io(io::Error::from(io::ErrorKind::BrokenPipe));
        let full = pcap_file::pcap::PcapWriteError::Io(io::Error::from(io::ErrorKind::StorageFull));

        assert!(is_broken_pipe(&piped));
        assert!(!is_broken_pipe(&full));
    }

    /// Verifies that a record the writer cannot encode ends the capture with
    /// an error instead of a log line.
    #[test]
    fn test_pdump_write_reports_a_failed_record() {
        let writer = PdumpWriter::new(DumpOutputFormat::Pcap, "/dev/null").expect("must open");
        let (tx, rx) = mpsc::channel(1);
        tx.try_send(pdumppb::Record {
            meta: None,
            data: Vec::new(),
            lost_before: 0,
        })
        .expect("must accept the record");
        drop(tx);

        let err =
            pdump_write(writer, rx, None, io::sink()).expect_err("a record without metadata must fail the capture");

        assert_eq!("failed to write record: pdump record missing metadata", err.to_string());
    }
}
