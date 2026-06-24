import React, { useState, useCallback, useEffect, useMemo, useRef } from 'react';
import { PacketTable, PacketInspector } from '@yanet/core/components/packet';
import type { PacketRow } from '@yanet/core/components/packet';
import { parsePacket, base64ToUint8Array } from '@yanet/core/utils/packetParser';
import { parsePcapFile } from '@yanet/core/utils/pcapFile';
import { toaster } from '@yanet/core/utils';
import type { DeviceDetailProps } from '@yanet/core/registry';
import { trafgen } from './api';
import { trafgenExt } from './types';

/** A pcap loaded locally for inspection but not yet uploaded to the device. */
interface PcapPreview {
    name: string;
    bytes: Uint8Array;
    frames: PacketRow[];
}

/** Encode a Uint8Array as base64 for the UploadPcap request body. */
const uint8ToBase64 = (bytes: Uint8Array): string => {
    let binary = '';
    for (let idx = 0; idx < bytes.length; idx++) {
        binary += String.fromCharCode(bytes[idx]);
    }
    return btoa(binary);
};

/** Decode base64 L2 frames from ShowPackets into displayable rows. */
const framesToPackets = (frames: string[] | undefined): PacketRow[] => {
    let idx = 0;
    return (frames ?? []).map(b64 => ({ id: idx++, parsed: parsePacket(base64ToUint8Array(b64)) }));
};

/** Parse raw .pcap file bytes locally into displayable rows for preview. */
const pcapToPackets = (bytes: Uint8Array): PacketRow[] => {
    let idx = 0;
    return parsePcapFile(bytes).map(frame => ({ id: idx++, parsed: parsePacket(frame) }));
};

/** Read a picked file as bytes, surfacing read errors as a toast. */
const readFileBytes = (file: File, onBytes: (bytes: Uint8Array) => void): void => {
    const reader = new FileReader();
    reader.onload = (ev) => {
        const buffer = ev.target?.result;
        if (buffer instanceof ArrayBuffer) {
            onBytes(new Uint8Array(buffer));
        }
    };
    reader.onerror = () => toaster.error('trafgen-pcap-read', 'Failed to read file.');
    reader.readAsArrayBuffer(file);
};

/** Generator controls + Frames + inspector panel for trafgen devices.
 *
 * Rate and pcap are applied live through SetRate / UploadPcap, each behind its
 * own button and independent of the device Save (which only writes pipelines).
 */
const TrafgenDetail = (props: DeviceDetailProps): React.JSX.Element => {
    const { device, onUpdateExt } = props;

    const ext = trafgenExt(device);
    const deviceName = device.id.name ?? '';
    const previewInputRef = useRef<HTMLInputElement>(null);

    // Live API calls target a server-created device; a not-yet-saved device
    // has no backend object to set a rate or upload frames into.
    const live = !device.isNew;

    const [selectedFrameId, setSelectedFrameId] = useState<number | null>(null);
    const [frameDrawerOpen, setFrameDrawerOpen] = useState(false);
    const [frameSearch, setFrameSearch] = useState('');
    const [framesOpen, setFramesOpen] = useState(true);
    const [rateInput, setRateInput] = useState(String(ext.ratePps ?? 0));
    const [applyingRate, setApplyingRate] = useState(false);
    const [uploading, setUploading] = useState(false);
    const [preview, setPreview] = useState<PcapPreview | null>(null);

    // Reset the frame inspector and any preview when the generator changes.
    useEffect(() => {
        setSelectedFrameId(null);
        setFrameDrawerOpen(false);
        setFrameSearch('');
        setPreview(null);
    }, [deviceName]);

    // Resync the rate field when the device changes or the server rate loads.
    useEffect(() => {
        setRateInput(String(ext.ratePps ?? 0));
    }, [deviceName, ext.ratePps]);

    const deviceFrames = ext.framePackets ?? [];
    // While a preview is staged the table shows its local frames; otherwise the
    // frames currently on the device.
    const effectiveFrames = preview ? preview.frames : deviceFrames;

    // Accept any keystrokes but only treat a plain natural number (digits, no
    // sign/decimal/exponent) as a valid rate; anything else blocks Apply.
    const trimmedRate = rateInput.trim();
    const rateValid = /^\d+$/.test(trimmedRate);
    const parsedRate = rateValid ? Number(trimmedRate) : NaN;
    const rateInvalid = trimmedRate.length > 0 && !rateValid;
    const rateModified = rateValid && parsedRate !== (ext.ratePps ?? 0);
    const canApplyRate = live && rateModified && !applyingRate;

    const handleApplyRate = useCallback(async () => {
        if (!canApplyRate) {
            return;
        }
        setApplyingRate(true);
        try {
            await trafgen.setRate({ name: deviceName, rate_pps: parsedRate });
            onUpdateExt({ ratePps: parsedRate });
            toaster.success(`trafgen-rate-${deviceName}`, `Rate set to ${parsedRate} pps`);
        } catch (err) {
            toaster.error(`trafgen-rate-${deviceName}`, 'Failed to set rate', err);
        } finally {
            setApplyingRate(false);
        }
    }, [canApplyRate, parsedRate, deviceName, onUpdateExt]);

    // Pushes pcap bytes to the device, then refreshes the on-device frame set
    // and clears any preview.
    const doUploadBytes = useCallback(async (bytes: Uint8Array, name: string) => {
        setUploading(true);
        try {
            await trafgen.uploadPcap({ name: deviceName, pcap: uint8ToBase64(bytes) });
            const pkts = await trafgen.showPackets(deviceName);
            onUpdateExt({
                framePackets: framesToPackets(pkts.packets),
                truncated: pkts.truncated ?? false,
            });
            setPreview(null);
            setSelectedFrameId(null);
            setFrameDrawerOpen(false);
            toaster.success(`trafgen-pcap-${deviceName}`, `Uploaded ${name}`);
        } catch (err) {
            toaster.error(`trafgen-pcap-${deviceName}`, 'Failed to upload pcap', err);
        } finally {
            setUploading(false);
        }
    }, [deviceName, onUpdateExt]);

    // Loads a pcap locally into a preview without touching the device.
    const handlePreviewPick = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (file) {
            readFileBytes(file, (bytes) => {
                try {
                    setPreview({ name: file.name, bytes, frames: pcapToPackets(bytes) });
                    setSelectedFrameId(null);
                    setFrameDrawerOpen(false);
                    setFramesOpen(true);
                } catch (err) {
                    const msg = err instanceof Error ? err.message : String(err);
                    toaster.error('trafgen-pcap-parse', `Failed to parse pcap: ${msg}`);
                }
            });
        }
        if (previewInputRef.current) {
            previewInputRef.current.value = '';
        }
    }, []);

    // Uploads the staged preview to the device. Loading a file is done via the
    // clickable frame-count box; this button only commits.
    const handleUpload = useCallback(() => {
        if (preview) {
            doUploadBytes(preview.bytes, preview.name);
        }
    }, [preview, doUploadBytes]);

    const handleDiscardPreview = useCallback(() => {
        setPreview(null);
        setSelectedFrameId(null);
        setFrameDrawerOpen(false);
    }, []);

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

    const sourceLabel = preview
        ? `${preview.name} · preview`
        : deviceFrames.length > 0
            ? `${deviceFrames.length} frames loaded`
            : 'No pcap loaded';
    const frameMeta = preview
        ? `${preview.frames.length} frames · preview`
        : `${deviceFrames.length} frames`;

    return (
        <>
            <div className="dv-section">
                <div className="dv-section-hd"><span>Generator</span></div>
                {!live && (
                    <div className="dv-gen-note">Save the device to enable live rate and pcap.</div>
                )}
                <div className="dv-gen-cards">
                    <div className="dv-gen-card">
                        <div className="dv-gen-card-hd">
                            <label className="dv-gen-card-title" htmlFor="dv-gen-rate">Transmit rate</label>
                            <span
                                className={"dv-gen-card-flag" + (rateInvalid ? ' dv-gen-card-flag--err' : '')}
                                style={{ visibility: rateModified || rateInvalid ? 'visible' : 'hidden' }}
                            >
                                {rateInvalid ? 'natural number only' : '● modified'}
                            </span>
                        </div>
                        <div className="dv-gen-card-row">
                            <input
                                id="dv-gen-rate"
                                className={"dv-gen-rate mono" + (rateInvalid ? ' dv-gen-rate--invalid' : '')}
                                type="text"
                                inputMode="numeric"
                                value={rateInput}
                                disabled={!live}
                                onChange={e => setRateInput(e.target.value)}
                                onKeyDown={e => { if (e.key === 'Enter') { handleApplyRate(); } }}
                            />
                            <span className="dv-gen-unit">pps</span>
                            <button
                                className={"btn-primary dv-gen-apply" + (canApplyRate ? '' : ' btn-primary-dim')}
                                onClick={handleApplyRate}
                                disabled={!canApplyRate}
                            >
                                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2">
                                    <polyline points="20,6 9,17 4,12" />
                                </svg>
                                {applyingRate ? 'Applying…' : 'Apply rate'}
                            </button>
                        </div>
                        <div className="dv-gen-card-note">
                            Target packets-per-second the generator emits. Apply rate pushes it to
                            the running device immediately — the Save button is only for pipelines.
                        </div>
                    </div>

                    <div className="dv-gen-card">
                        <div className="dv-gen-card-hd">
                            <span className="dv-gen-card-title">Packet source</span>
                            <span className="dv-gen-card-meta mono">{frameMeta}</span>
                        </div>
                        <div className="dv-gen-card-row">
                            <button
                                type="button"
                                className={"dv-gen-file" + (preview ? ' dv-gen-file--preview' : '')}
                                onClick={() => previewInputRef.current?.click()}
                                title="Load a pcap to preview its frames"
                            >
                                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
                                    <path d="M6 3h8l4 4v14H6Z" /><polyline points="14,3 14,7 18,7" />
                                </svg>
                                <span className="dv-gen-file-name mono">{sourceLabel}</span>
                            </button>
                            <input
                                ref={previewInputRef}
                                type="file"
                                accept=".pcap,.pcapng"
                                style={{ display: 'none' }}
                                onChange={handlePreviewPick}
                            />
                            <button
                                className="btn-secondary"
                                onClick={handleUpload}
                                disabled={!live || uploading || !preview}
                            >
                                {uploading ? 'Uploading…' : 'Upload to device'}
                            </button>
                        </div>
                        {preview ? (
                            <div className="dv-gen-preview-note">
                                <span>Previewing <span className="mono">{preview.name}</span> — not uploaded.</span>
                                <button className="dv-gen-link" onClick={handleDiscardPreview}>Discard</button>
                            </div>
                        ) : (
                            <div className="dv-gen-card-note">
                                Click the frame count to load and preview a pcap, then “Upload to
                                device” to apply it live — no Save required.
                            </div>
                        )}
                    </div>
                </div>
                {!preview && ext.truncated && (
                    <div className="dv-gen-note">The frame list shows a capped subset of the uploaded frames.</div>
                )}
            </div>

            <div className="dv-section">
                <button
                    className="dv-frames-toggle"
                    onClick={() => setFramesOpen(v => !v)}
                    aria-expanded={framesOpen}
                >
                    <svg
                        className={"dv-frames-chevron" + (framesOpen ? ' open' : '')}
                        width="14" height="14" viewBox="0 0 24 24"
                        fill="none" stroke="currentColor" strokeWidth="2.2"
                    >
                        <polyline points="9,6 15,12 9,18" />
                    </svg>
                    <span className="dv-frames-toggle-lbl">Frames</span>
                    <span className="dv-frames-toggle-count mono">
                        {effectiveFrames.length} packet{effectiveFrames.length !== 1 ? 's' : ''}
                    </span>
                    <span className="dv-frames-toggle-hint">{framesOpen ? 'Hide' : 'Show'}</span>
                </button>
                {framesOpen && (
                    <div className="dv-gen-frames">
                        <PacketTable
                            key={(deviceName || 'empty') + (preview ? ':preview' : '')}
                            packets={effectiveFrames}
                            selectedPacketId={selectedFrameId}
                            onSelectPacket={handleSelectFrame}
                            searchQuery={frameSearch}
                            onSearchQueryChange={setFrameSearch}
                            isLive={false}
                            emptyMessage="No frames loaded. Load a pcap file to populate them."
                            emptyFilterMessage="No frames match the filter."
                            showTimeColumn={false}
                            showToolbarActions={false}
                            footerNote={<span>{effectiveFrames.length > 0 ? 'Click to inspect' : ''}</span>}
                        />
                    </div>
                )}
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
