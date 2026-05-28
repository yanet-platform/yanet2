import React, { useCallback, useEffect, useState } from 'react';
import { ChevronDown } from '@gravity-ui/icons';
import { Icon } from '@gravity-ui/uikit';
import { formatTCPFlags } from '../../../utils';
import type { ParsedPacket } from '../../../utils/packetParser';
import type { CapturedPacket } from './types';

interface PacketDrawerProps {
    open: boolean;
    packet: CapturedPacket | null;
    packetIndex: number;
    totalPackets: number;
    configName?: string;
    onClose: () => void;
    onPrev: () => void;
    onNext: () => void;
}

type SectionKind = 'meta' | 'eth' | 'vlan' | 'ipv4' | 'ipv6' | 'tcp' | 'udp' | 'icmp' | 'http';

interface SectionProps {
    kind: SectionKind;
    title: string;
    sub?: string;
    children: React.ReactNode;
}

const Section: React.FC<SectionProps> = ({ kind, title, sub, children }) => {
    const [collapsed, setCollapsed] = useState(false);

    return (
        <div className={`pdump-section pdump-section--${kind}${collapsed ? ' pdump-section--collapsed' : ''}`}>
            <div className="pdump-section__head" onClick={() => setCollapsed(c => !c)}>
                <div className="pdump-section__head-left">
                    <span className="pdump-section__marker" />
                    <span className="pdump-section__title">{title}</span>
                    {sub && <span className="pdump-section__sub">{sub}</span>}
                </div>
                <span className="pdump-section__chevron">
                    <Icon data={ChevronDown} size={14} />
                </span>
            </div>
            <div className="pdump-section__body">
                {children}
            </div>
        </div>
    );
};

interface KVProps {
    k: string;
    v: React.ReactNode;
}

const KV: React.FC<KVProps> = ({ k, v }) => (
    <div className="pdump-kv-row">
        <span className="pdump-kv-k">{k}</span>
        <span className="pdump-kv-v">{v}</span>
    </div>
);

interface HexDumpProps {
    bytes: Uint8Array;
    parsed: ParsedPacket;
}

const HexDump: React.FC<HexDumpProps> = ({ bytes, parsed }) => {
    const ethEnd = 14 + (parsed.vlans?.length ?? 0) * 4;
    const ipEnd = parsed.ipv4
        ? ethEnd + parsed.ipv4.ihl * 4
        : parsed.ipv6
        ? ethEnd + 40
        : ethEnd;
    const l4End = parsed.payloadOffset;

    const rows: React.ReactNode[] = [];
    for (let i = 0; i < bytes.length; i += 16) {
        const slice = bytes.slice(i, i + 16);
        const offset = i.toString(16).padStart(4, '0');

        const hexSpans: React.ReactNode[] = [];
        const asciiChars: string[] = [];

        slice.forEach((b, j) => {
            const idx = i + j;
            let cls = 'hl-payload';
            if (idx < ethEnd) cls = 'hl-eth';
            else if (idx < ipEnd) cls = 'hl-ip';
            else if (idx < l4End) cls = 'hl-l4';

            hexSpans.push(<span key={j} className={cls}>{b.toString(16).padStart(2, '0')}</span>);
            hexSpans.push(' ');

            const ch = b >= 32 && b < 127 ? String.fromCharCode(b) : '.';
            asciiChars.push(ch);
        });

        rows.push(
            <div className="pdump-hex-line" key={i}>
                <span className="pdump-hex-offset">{offset}</span>
                <span className="pdump-hex-bytes">{hexSpans}</span>
                <span className="pdump-hex-ascii">{asciiChars.join('')}</span>
            </div>
        );
    }

    return <div className="pdump-hex-dump">{rows}</div>;
};

const formatTime = (date: Date): string =>
    date.toLocaleTimeString('en-US', {
        hour12: false,
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        fractionalSecondDigits: 3,
    } as Intl.DateTimeFormatOptions);

/**
 * Right-side drawer for inspecting a captured packet.
 * Keyboard: Esc closes, arrow-up / k = previous, arrow-down / j = next.
 */
const PacketDrawer: React.FC<PacketDrawerProps> = ({
    open,
    packet,
    packetIndex,
    totalPackets,
    configName,
    onClose,
    onPrev,
    onNext,
}) => {
    const handleKeyDown = useCallback((e: KeyboardEvent) => {
        const target = document.activeElement;
        if (target instanceof HTMLElement) {
            if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) return;
        }
        switch (e.key) {
            case 'Escape':
                onClose();
                break;
            case 'ArrowUp':
            case 'k':
                e.preventDefault();
                onPrev();
                break;
            case 'ArrowDown':
            case 'j':
                e.preventDefault();
                onNext();
                break;
        }
    }, [onClose, onPrev, onNext]);

    useEffect(() => {
        if (!open) return;
        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [open, handleKeyDown]);

    const tcp = packet?.parsed.tcp;
    const udp = packet?.parsed.udp;

    const tcpFlagTokens = (() => {
        if (!tcp) return null;
        const raw = formatTCPFlags(tcp.flags);
        if (!raw) return <span>none</span>;
        const flags = raw.replace(/^\[|\]$/g, '').split(', ').filter(Boolean);
        return (
            <>
                {flags.map((f, idx) => (
                    <span key={idx} className="pdump-kv-flag">{f}</span>
                ))}
            </>
        );
    })();

    return (
        <aside
            className={`fw-drawer${open ? ' fw-drawer--open' : ''}`}
            role="dialog"
            aria-modal="true"
            aria-label="Packet details"
        >
            <header className="fw-drawer__head">
                <h2 className="fw-drawer__title">
                    {packet ? (
                        <>
                            Packet <span className="fw-drawer__rule-num">#{packet.id + 1}</span>
                            <span className="pdump-drawer-timestamp">{formatTime(packet.timestamp)}</span>
                        </>
                    ) : (
                        'Packet details'
                    )}
                </h2>
                <div className="fw-drawer__head-actions">
                    <button
                        type="button"
                        className="fw-icon-btn"
                        onClick={onPrev}
                        disabled={packetIndex <= 0}
                        title="Previous packet (↑ / k)"
                        aria-label="Previous packet"
                    >
                        ↑
                    </button>
                    <button
                        type="button"
                        className="fw-icon-btn"
                        onClick={onNext}
                        disabled={packetIndex < 0 || packetIndex >= totalPackets - 1}
                        title="Next packet (↓ / j)"
                        aria-label="Next packet"
                    >
                        ↓
                    </button>
                    <button
                        type="button"
                        className="fw-icon-btn"
                        onClick={onClose}
                        aria-label="Close drawer"
                        title="Close (Esc)"
                    >
                        ✕
                    </button>
                </div>
            </header>

            <div className="fw-drawer__body">
                {packet ? (
                    <>
                        <Section kind="meta" title="Capture" sub={configName ?? ''}>
                            <KV k="Worker" v={packet.record.meta?.worker_idx ?? 'N/A'} />
                            <KV k="Queue" v={packet.record.meta?.queue ?? 'N/A'} />
                            <KV k="RX device" v={packet.record.meta?.rx_device_id ?? 'N/A'} />
                            <KV k="TX device" v={packet.record.meta?.tx_device_id ?? 'N/A'} />
                            <KV
                                k="Length"
                                v={`${packet.record.meta?.packet_len ?? packet.parsed.raw.length} bytes (captured ${packet.record.meta?.data_size ?? packet.parsed.raw.length})`}
                            />
                        </Section>

                        {packet.parsed.ethernet && (
                            <Section kind="eth" title="Ethernet II">
                                <KV k="Source MAC" v={packet.parsed.ethernet.srcMac} />
                                <KV k="Dest MAC" v={packet.parsed.ethernet.dstMac} />
                                <KV k="EtherType" v={`${packet.parsed.ethernet.etherTypeName} (0x${packet.parsed.ethernet.etherType.toString(16)})`} />
                            </Section>
                        )}

                        {packet.parsed.vlans && packet.parsed.vlans.length > 0 && (
                            <Section kind="vlan" title="VLAN / 802.1Q" sub={`${packet.parsed.vlans.length} tag${packet.parsed.vlans.length > 1 ? 's' : ''}`}>
                                {packet.parsed.vlans.map((tag, idx) => (
                                    <React.Fragment key={idx}>
                                        <KV k="Tag" v={`#${idx + 1}`} />
                                        <KV k="TPID" v={`${tag.tpidName} (0x${tag.tpid.toString(16)})`} />
                                        <KV k="TCI" v={`0x${tag.tci.toString(16).padStart(4, '0')}`} />
                                        <KV k="PCP" v={tag.pcp} />
                                        <KV k="DEI" v={tag.dei ? '1' : '0'} />
                                        <KV k="VLAN ID" v={tag.vlanId} />
                                        <KV k="Inner EtherType" v={`${tag.innerEtherTypeName} (0x${tag.innerEtherType.toString(16)})`} />
                                    </React.Fragment>
                                ))}
                            </Section>
                        )}

                        {packet.parsed.ipv4 && (
                            <Section kind="ipv4" title="Internet Protocol v4">
                                <KV k="Source" v={packet.parsed.ipv4.srcAddr} />
                                <KV k="Destination" v={packet.parsed.ipv4.dstAddr} />
                                <KV k="Protocol" v={`${packet.parsed.ipv4.protocolName} (${packet.parsed.ipv4.protocol})`} />
                                <KV k="TTL" v={packet.parsed.ipv4.ttl} />
                                <KV k="Total length" v={packet.parsed.ipv4.totalLength} />
                                <KV k="ID" v={`0x${packet.parsed.ipv4.identification.toString(16)}`} />
                                <KV k="DSCP" v={packet.parsed.ipv4.dscp} />
                                <KV k="ECN" v={packet.parsed.ipv4.ecn} />
                                <KV k="Don't fragment" v={packet.parsed.ipv4.flags.dontFragment ? 'true' : 'false'} />
                                <KV k="More fragments" v={packet.parsed.ipv4.flags.moreFragments ? 'true' : 'false'} />
                                <KV k="Fragment offset" v={packet.parsed.ipv4.fragmentOffset} />
                            </Section>
                        )}

                        {packet.parsed.ipv6 && (
                            <Section kind="ipv6" title="Internet Protocol v6">
                                <KV k="Source" v={packet.parsed.ipv6.srcAddr} />
                                <KV k="Destination" v={packet.parsed.ipv6.dstAddr} />
                                <KV k="Next header" v={`${packet.parsed.ipv6.nextHeaderName} (${packet.parsed.ipv6.nextHeader})`} />
                                <KV k="Hop limit" v={packet.parsed.ipv6.hopLimit} />
                                <KV k="Payload len" v={packet.parsed.ipv6.payloadLength} />
                                <KV k="Flow label" v={`0x${packet.parsed.ipv6.flowLabel.toString(16)}`} />
                            </Section>
                        )}

                        {tcp && (
                            <Section kind="tcp" title="Transmission Control Protocol" sub={`${tcp.srcPort} → ${tcp.dstPort}`}>
                                <KV k="Source port" v={tcp.srcPort} />
                                <KV k="Dest port" v={tcp.dstPort} />
                                <KV k="Sequence" v={tcp.seqNum} />
                                <KV k="Acknowledgment" v={tcp.ackNum} />
                                <KV k="Window" v={tcp.windowSize} />
                                <KV k="Flags" v={tcpFlagTokens} />
                                <KV k="Checksum" v={`0x${tcp.checksum.toString(16)}`} />
                            </Section>
                        )}

                        {udp && (
                            <Section kind="udp" title="User Datagram Protocol" sub={`${udp.srcPort} → ${udp.dstPort}`}>
                                <KV k="Source port" v={udp.srcPort} />
                                <KV k="Dest port" v={udp.dstPort} />
                                <KV k="Length" v={udp.length} />
                                <KV k="Checksum" v={`0x${udp.checksum.toString(16)}`} />
                            </Section>
                        )}

                        {packet.parsed.icmp && (
                            <Section kind="icmp" title="ICMP">
                                <KV k="Type" v={`${packet.parsed.icmp.typeName} (${packet.parsed.icmp.type})`} />
                                <KV k="Code" v={packet.parsed.icmp.code} />
                                <KV k="Checksum" v={`0x${packet.parsed.icmp.checksum.toString(16)}`} />
                            </Section>
                        )}

                        {packet.parsed.http && (() => {
                            const http = packet.parsed.http;
                            const sub = http.isRequest
                                ? `${http.method} ${http.target}`
                                : `${http.statusCode} ${http.reasonPhrase}`;
                            return (
                                <Section kind="http" title="Hypertext Transfer Protocol" sub={sub}>
                                    {http.isRequest ? (
                                        <>
                                            <KV k="Method" v={http.method} />
                                            <KV k="Target" v={http.target} />
                                            <KV k="Version" v={http.version} />
                                        </>
                                    ) : (
                                        <>
                                            <KV k="Version" v={http.version} />
                                            <KV k="Status" v={http.statusCode} />
                                            <KV k="Reason" v={http.reasonPhrase} />
                                        </>
                                    )}
                                    {http.headers.map((h, idx) => (
                                        <KV key={idx} k={h.name} v={h.value} />
                                    ))}
                                </Section>
                            );
                        })()}

                        <Section kind="meta" title="Raw bytes" sub={`${packet.parsed.raw.length} B`}>
                            <HexDump bytes={packet.parsed.raw} parsed={packet.parsed} />
                        </Section>
                    </>
                ) : (
                    <div className="pdump-drawer-empty">
                        Select a packet to inspect.
                    </div>
                )}
            </div>

            {packet && (
                <footer className="fw-drawer__foot">
                    <span className="fw-drawer__foot-meta">
                        {packetIndex >= 0
                            ? `${packetIndex + 1} / ${totalPackets} packets`
                            : `evicted / ${totalPackets} packets`}
                    </span>
                    <div className="fw-drawer__foot-actions">
                        <button type="button" className="fw-btn fw-btn--ghost" onClick={onClose}>
                            Close
                        </button>
                    </div>
                </footer>
            )}
        </aside>
    );
};

export default React.memo(PacketDrawer);
