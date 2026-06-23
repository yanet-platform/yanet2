import React, { useState, useCallback, useEffect, useMemo, useRef } from 'react';
import { PacketTable, PacketInspector } from '@yanet/core/components/packet';
import type { PacketRow } from '@yanet/core/components/packet';
import { parsePacket } from '@yanet/core/utils/packetParser';
import { parsePcapFile } from '@yanet/core/utils/pcapFile';
import { toaster } from '@yanet/core/utils';
import type { DeviceDetailProps } from '@yanet/core/registry';
import { trafgenExt } from './types';

/** Generator controls + Frames + inspector panel for trafgen devices. */
const TrafgenDetail = (props: DeviceDetailProps): React.JSX.Element => {
    const { device, onUpdateExt } = props;

    // Read via the helper, which supplies defaults: a server-loaded generator
    // has no ext slice until loadDeviceExt hydrates it asynchronously.
    const ext = trafgenExt(device);

    const deviceName = device.id.name ?? '';
    const fileInputRef = useRef<HTMLInputElement>(null);

    const [selectedFrameId, setSelectedFrameId] = useState<number | null>(null);
    const [frameDrawerOpen, setFrameDrawerOpen] = useState(false);
    const [frameSearch, setFrameSearch] = useState('');

    // Reset the frame inspector whenever the selected generator changes.
    useEffect(() => {
        setSelectedFrameId(null);
        setFrameDrawerOpen(false);
        setFrameSearch('');
    }, [deviceName]);

    const handleRateChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        const val = parseInt(e.target.value, 10);
        onUpdateExt({ ratePps: isNaN(val) ? 0 : Math.max(0, val) });
    }, [onUpdateExt]);

    const handleChoosePcap = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (!file) { return; }
        const reader = new FileReader();
        reader.onload = (ev) => {
            try {
                const buffer = ev.target?.result;
                if (!(buffer instanceof ArrayBuffer)) { return; }
                const bytes = new Uint8Array(buffer);
                const frames = parsePcapFile(bytes);
                let idx = 0;
                const rows: PacketRow[] = frames.map(frame => ({ id: idx++, parsed: parsePacket(frame) }));
                onUpdateExt({ stagedPcapBytes: bytes, stagedFramePackets: rows });
                setSelectedFrameId(null);
                setFrameDrawerOpen(false);
            } catch (err) {
                const msg = err instanceof Error ? err.message : String(err);
                toaster.error('trafgen-pcap-parse', `Failed to parse pcap: ${msg}`);
            }
        };
        reader.onerror = () => toaster.error('trafgen-pcap-read', 'Failed to read file.');
        reader.readAsArrayBuffer(file);
        if (fileInputRef.current) {
            fileInputRef.current.value = '';
        }
    }, [onUpdateExt]);

    const handleCancelStagedPcap = useCallback(() => {
        onUpdateExt({ stagedPcapBytes: null, stagedFramePackets: undefined });
        setSelectedFrameId(null);
        setFrameDrawerOpen(false);
    }, [onUpdateExt]);

    const effectiveFrames = useMemo(
        () => ext.stagedFramePackets ?? ext.framePackets ?? [],
        [ext.stagedFramePackets, ext.framePackets],
    );

    const handleSelectFrame = useCallback((packet: PacketRow | null) => {
        if (packet) {
            setSelectedFrameId(packet.id);
            setFrameDrawerOpen(true);
        } else {
            setSelectedFrameId(null);
            setFrameDrawerOpen(false);
        }
    }, []);

    const selectedFrameIndex = useMemo(
        () => effectiveFrames.findIndex(p => p.id === selectedFrameId),
        [effectiveFrames, selectedFrameId],
    );

    const inspectorFrame = selectedFrameId !== null
        ? effectiveFrames.find(p => p.id === selectedFrameId) ?? null
        : null;

    const stagedFrameCount = ext.stagedFramePackets?.length ?? 0;
    const pcapStaged = ext.stagedPcapBytes != null;

    return (
        <>
            <div className="dv-section">
                <div className="dv-section-hd"><span>Generator</span></div>
                <div className="dv-gen-controls">
                    <div className="dv-gen-group">
                        <label className="dv-gen-lbl" htmlFor="dv-gen-rate">Rate</label>
                        <input
                            id="dv-gen-rate"
                            className="dv-gen-rate mono"
                            type="number"
                            min={0}
                            step={1}
                            value={String(ext.ratePps ?? 0)}
                            onChange={handleRateChange}
                        />
                        <span className="dv-gen-unit">pps</span>
                    </div>
                    <div className="dv-gen-group">
                        <input
                            ref={fileInputRef}
                            type="file"
                            accept=".pcap,.pcapng"
                            style={{ display: 'none' }}
                            onChange={handleChoosePcap}
                        />
                        <button className="btn-secondary" onClick={() => fileInputRef.current?.click()}>
                            Choose pcap…
                        </button>
                        {pcapStaged && (
                            <>
                                <span className="dv-gen-hint">
                                    {stagedFrameCount} frame{stagedFrameCount !== 1 ? 's' : ''} staged · saves on Save
                                </span>
                                <button className="btn-secondary" onClick={handleCancelStagedPcap}>
                                    Cancel
                                </button>
                            </>
                        )}
                    </div>
                </div>
                {ext.truncated && !pcapStaged && (
                    <div className="dv-gen-note">The preview shows a capped subset of the uploaded frames.</div>
                )}
            </div>

            <div className="dv-section">
                <div className="dv-section-hd"><span>Frames</span></div>
                <div className="dv-gen-frames">
                    <PacketTable
                        key={deviceName || 'empty'}
                        packets={effectiveFrames}
                        selectedPacketId={selectedFrameId}
                        onSelectPacket={handleSelectFrame}
                        searchQuery={frameSearch}
                        onSearchQueryChange={setFrameSearch}
                        isLive={false}
                        emptyMessage="No frames loaded. Choose a pcap file to stage one."
                        emptyFilterMessage="No frames match the filter."
                        showTimeColumn={false}
                        showToolbarActions={false}
                        footerNote={<span>{effectiveFrames.length > 0 ? 'Click to inspect' : ''}</span>}
                    />
                </div>
            </div>

            <PacketInspector
                open={frameDrawerOpen}
                packet={inspectorFrame}
                packetIndex={selectedFrameIndex}
                totalPackets={effectiveFrames.length}
                onClose={() => { setSelectedFrameId(null); setFrameDrawerOpen(false); }}
                onPrev={() => {
                    const idx = effectiveFrames.findIndex(p => p.id === selectedFrameId);
                    if (idx > 0) { setSelectedFrameId(effectiveFrames[idx - 1].id); }
                }}
                onNext={() => {
                    const idx = effectiveFrames.findIndex(p => p.id === selectedFrameId);
                    if (idx >= 0 && idx < effectiveFrames.length - 1) { setSelectedFrameId(effectiveFrames[idx + 1].id); }
                }}
                title="Frame"
            />
        </>
    );
};

export default TrafgenDetail;
