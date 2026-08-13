import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import {
    Button,
    Icon,
    Label,
    SegmentedRadioGroup,
    Select,
    Switch,
    Text,
    TextInput,
    Tooltip,
} from '@gravity-ui/uikit';
import { CircleInfo, Plus } from '@gravity-ui/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useConfigListCache, useSearchParamHelpers, usePageContribution, useContainerHeight, useTabCycle, useUnsavedChangesBlocker } from '@yanet/core/hooks';
import { API, inventoryConfigNames, loadKnownConfigs, unionConfigNames } from '@yanet/core/api';
import { Direction, MapKind, type FwStateEntry, type ListEntriesRequest, type MapStats } from '@yanet/core/api/fwstatemap';
import { ConfigTabStrip, PageLayout, PageLoader, EmptyPagePlaceholder } from '@yanet/core/components';
import { ipAddressToString, isValidIPAddress, parseIPToBytes, stringToIPAddress, type IPAddressWire } from '@yanet/core/utils/netip';
import { formatBytes, toaster, compareNatural, warnConfigsUnknown } from '@yanet/core/utils';
import { AddConfigModal, CommandPaletteHeader, ConfirmModal, DeleteConfigModal } from '@yanet/core/components';
import { SaveIcon, TrashIcon } from '@yanet/core/components/draft';
import type { Command, PagePaletteContribution } from '@yanet/core/components/command-palette';
import '@yanet/core/styles/chrome.scss';
import './fwstate.scss';

interface DraftConfig {
    mapNameV4: string;
    mapNameV6: string;
    srcAddr: string;
    dstAddrMulticast: string;
    portMulticast: number;
    tcpSynAck: string;
    tcpSyn: string;
    tcpFin: string;
    tcp: string;
    udp: string;
    defaultTimeout: string;
    isLocalOnly: boolean;
}

const DEFAULT_NS = {
    tcpSynAck: 120_000_000_000,
    tcpSyn: 120_000_000_000,
    tcpFin: 120_000_000_000,
    tcp: 120_000_000_000,
    udp: 30_000_000_000,
    defaultTimeout: 16_000_000_000,
};

const STATES_TABLE_ROW_HEIGHT = 38;
const STATES_TABLE_OVERSCAN = 12;
const STATES_TABLE_LOAD_THRESHOLD = 60;
const STATES_TABLE_MAX_BATCH_SIZE = 10000;
const BACKWARD_RESET_CURSOR = Number.MAX_SAFE_INTEGER;
const STATES_CURSORBAR_HEIGHT = 41;

const formatDurationNsAsSeconds = (value: number): string => {
    if (!Number.isFinite(value) || value <= 0) return '';
    const seconds = value / 1_000_000_000;
    if (Number.isInteger(seconds)) return String(seconds);
    return seconds.toFixed(9).replace(/\.?0+$/, '');
};

const parseDurationToNs = (value: string): number | null => {
    const trimmed = value.trim().toLowerCase();
    if (!trimmed) return null;
    const numberOnly = trimmed.match(/^\d+(?:\.\d+)?$/);
    if (numberOnly) {
        const seconds = Number(trimmed);
        if (!Number.isFinite(seconds) || seconds <= 0) return null;
        return Math.round(seconds * 1_000_000_000);
    }
    const unitMatch = trimmed.match(/^(\d+(?:\.\d+)?)(ns|ms|s|m|h)$/);
    if (!unitMatch) return null;
    const amount = Number(unitMatch[1]);
    if (!Number.isFinite(amount) || amount <= 0) return null;
    const unit = unitMatch[2];
    if (unit === 'ns') return Math.round(amount);
    if (unit === 'ms') return Math.round(amount * 1_000_000);
    if (unit === 's') return Math.round(amount * 1_000_000_000);
    if (unit === 'm') return Math.round(amount * 60 * 1_000_000_000);
    return Math.round(amount * 3600 * 1_000_000_000);
};

const isValidIPv6Address = (value: string): boolean => {
    return isValidIPAddress(value) && value.includes(':');
};

const isZeroIPv6Address = (value: string): boolean => {
    const bytes = parseIPToBytes(value);
    return Boolean(bytes && bytes.length === 16 && bytes.every((byte) => byte === 0));
};

const isValidNonzeroIPv6Address = (value: string): boolean => {
    return isValidIPv6Address(value) && !isZeroIPv6Address(value);
};

const toDraftConfig = (config: Awaited<ReturnType<typeof API.fwstate.showConfig>> | null, isLocalOnly: boolean): DraftConfig => {
    const sync = config?.sync_config;
    return {
        mapNameV4: config?.map_name_v4 ?? '',
        mapNameV6: config?.map_name_v6 ?? '',
        srcAddr: ipAddressToString(sync?.src_addr as IPAddressWire | undefined),
        dstAddrMulticast: ipAddressToString(sync?.dst_addr_multicast as IPAddressWire | undefined),
        portMulticast: sync?.port_multicast ?? 0,
        tcpSynAck: formatDurationNsAsSeconds(sync?.tcp_syn_ack ?? DEFAULT_NS.tcpSynAck),
        tcpSyn: formatDurationNsAsSeconds(sync?.tcp_syn ?? DEFAULT_NS.tcpSyn),
        tcpFin: formatDurationNsAsSeconds(sync?.tcp_fin ?? DEFAULT_NS.tcpFin),
        tcp: formatDurationNsAsSeconds(sync?.tcp ?? DEFAULT_NS.tcp),
        udp: formatDurationNsAsSeconds(sync?.udp ?? DEFAULT_NS.udp),
        defaultTimeout: formatDurationNsAsSeconds(sync?.default ?? DEFAULT_NS.defaultTimeout),
        isLocalOnly,
    };
};

const normalizeUnsignedInt = (value: number | string | null | undefined): string | null => {
    if (value === undefined || value === null) return null;
    if (typeof value === 'number') {
        if (!Number.isFinite(value) || !Number.isInteger(value) || value < 0) {
            return null;
        }
        return String(value);
    }
    const trimmed = value.trim();
    if (!trimmed) return null;
    if (!/^\d+$/.test(trimmed)) return null;
    return trimmed.replace(/^0+(?=\d)/, '');
};

const normalizeUnsignedIntToNumber = (value: number | string | null | undefined): number => {
    const normalized = normalizeUnsignedInt(value);
    if (!normalized) return 0;
    const parsed = Number(normalized);
    if (!Number.isFinite(parsed) || !Number.isInteger(parsed) || parsed < 0) return 0;
    if (parsed > Number.MAX_SAFE_INTEGER) return Number.MAX_SAFE_INTEGER;
    return parsed;
};

const fmtInt = (n: number): string => n.toLocaleString('en-US');

const fmtCompact = (n: number): string => {
    if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 1 : 2) + 'M';
    if (n >= 1e3) return (n / 1e3).toFixed(n >= 1e4 ? 1 : 2) + 'k';
    return String(n);
};

const formatNsUtc = (value: number | string | null | undefined): string => {
    const ns = normalizeUnsignedInt(value);
    if (!ns || ns === '0') return '-';
    try {
        const millis = Number(BigInt(ns) / 1_000_000n);
        const date = new Date(millis);
        if (!Number.isFinite(date.getTime())) {
            return '-';
        }
        return date.toISOString();
    } catch {
        return '-';
    }
};

const formatStateIdx = (idx: number | string | null | undefined): string => {
    if (idx === undefined || idx === null) return '0';
    return normalizeUnsignedInt(idx) ?? '-';
};

const formatMemoryBytes = (value: number | string | null | undefined): string => {
    try {
        if (value === undefined || value === null) return '-';
        if (typeof value === 'number') {
            if (!Number.isFinite(value) || !Number.isInteger(value) || !Number.isSafeInteger(value) || value < 0) {
                return '-';
            }
            return formatBytes(BigInt(value));
        }
        const trimmed = value.trim();
        if (!trimmed || !/^\d+$/.test(trimmed)) return '-';
        return formatBytes(BigInt(trimmed));
    } catch {
        return '-';
    }
};

const FLAG_TONES: Array<{ bit: number; label: string }> = [
    { bit: 0x01, label: 'FIN' },
    { bit: 0x02, label: 'SYN' },
    { bit: 0x04, label: 'RST' },
    { bit: 0x08, label: 'ACK' },
];

const QP_TAB = 'tab';
const QP_CONFIG = 'config';
const QP_FAMILY = 'family';
const QP_DIRECTION = 'direction';
const QP_LAYER = 'layer';
const QP_EXPIRED = 'expired';

interface StatesQuery {
    isIpv6: boolean;
    layerIndex: number;
    direction: Direction;
    includeExpired: boolean;
}

const getStatesQuery = (params: URLSearchParams): StatesQuery => {
    const familyValue = params.get(QP_FAMILY);
    const directionValue = params.get(QP_DIRECTION);
    const layerValue = params.get(QP_LAYER);
    const expiredValue = params.get(QP_EXPIRED);

    const isIpv6 = familyValue === 'ipv4' ? false : true;
    const direction = directionValue === 'backward' ? Direction.BACKWARD : Direction.FORWARD;
    const layerIndex = (() => {
        const normalized = normalizeUnsignedInt(layerValue);
        if (!normalized) return 0;
        return normalizeUnsignedIntToNumber(normalized);
    })();
    const includeExpired = expiredValue === '1';

    return {
        isIpv6,
        layerIndex,
        direction,
        includeExpired,
    };
};

const getStatesQueryParamValues = (query: StatesQuery): Record<string, string | null> => {
    return {
        [QP_FAMILY]: query.isIpv6 ? 'ipv6' : 'ipv4',
        [QP_DIRECTION]: query.direction === Direction.BACKWARD ? 'backward' : null,
        [QP_LAYER]: query.layerIndex > 0 ? String(query.layerIndex) : null,
        [QP_EXPIRED]: query.includeExpired ? '1' : null,
    };
};

const getStatesQueryParamUpdates = (params: URLSearchParams, query: StatesQuery): Record<string, string | null> => {
    const normalized = getStatesQueryParamValues(query);
    const updates: Record<string, string | null> = {};

    if (params.get(QP_FAMILY) !== normalized[QP_FAMILY]) {
        updates[QP_FAMILY] = normalized[QP_FAMILY];
    }
    if (params.get(QP_DIRECTION) !== normalized[QP_DIRECTION]) {
        updates[QP_DIRECTION] = normalized[QP_DIRECTION];
    }
    if (params.get(QP_LAYER) !== normalized[QP_LAYER]) {
        updates[QP_LAYER] = normalized[QP_LAYER];
    }
    if (params.get(QP_EXPIRED) !== normalized[QP_EXPIRED]) {
        updates[QP_EXPIRED] = normalized[QP_EXPIRED];
    }

    return updates;
};

type StateSubTab = 'configuration' | 'states' | 'statistics';

const STATE_SUB_TABS: Array<{ id: StateSubTab; label: string }> = [
    { id: 'configuration', label: 'Configuration' },
    { id: 'states', label: 'States' },
    { id: 'statistics', label: 'Statistics' },
];

const isStateSubTab = (value: string | null): value is StateSubTab => {
    return STATE_SUB_TABS.some((tab) => tab.id === value);
};

const getStateSubTab = (params: URLSearchParams): StateSubTab => {
    const value = params.get(QP_TAB);
    return isStateSubTab(value) ? value : 'states';
};

const decodeFlags = (rawFlags: number | string | null | undefined): { source: string[]; destination: string[] } => {
    const value = typeof rawFlags === 'number' ? rawFlags : Number(rawFlags);
    if (!Number.isInteger(value) || value < 0) {
        return { source: [], destination: [] };
    }
    const sourceFlagsValue = value & 0x0f;
    const destinationFlagsValue = (value >> 4) & 0x0f;
    const sourceFlags = FLAG_TONES
        .filter((flag) => sourceFlagsValue & flag.bit)
        .map((flag) => flag.label);
    const destinationFlags = FLAG_TONES
        .filter((flag) => destinationFlagsValue & flag.bit)
        .map((flag) => flag.label);
    return { source: sourceFlags, destination: destinationFlags };
};

const FLAG_ABBR: Record<string, string> = { FIN: 'F', SYN: 'S', RST: 'R', ACK: 'A' };

const renderFlagChips = (flags: string[]): React.ReactElement => {
    if (flags.length === 0) {
        return <span className="fws-flag-chip fws-flag-chip--none">—</span>;
    }
    return (
        <span className="fws-flag-chip-list">
            {flags.map((flag) => (
                <span key={flag} className="fws-flag-chip" title={flag} aria-label={flag}>
                    {FLAG_ABBR[flag] ?? flag}
                </span>
            ))}
        </span>
    );
};

const protoLabel = (proto: number | undefined): string => {
    if (proto === 6) return 'TCP';
    if (proto === 17) return 'UDP';
    if (proto === 1) return 'ICMP';
    if (proto === 58) return 'ICMPv6';
    return proto !== undefined ? String(proto) : '?';
};

const protoClass = (proto: number | undefined): string => {
    if (proto === 6) return 'fws-badge fws-badge--tcp';
    if (proto === 17) return 'fws-badge fws-badge--udp';
    if (proto === 1 || proto === 58) return 'fws-badge fws-badge--icmp';
    return 'fws-badge fws-badge--other';
};

const ageColorStyle = (ageMs: number): string => {
    if (ageMs < 30_000) return 'var(--fws-age-fresh)';
    if (ageMs < 300_000) return 'var(--fws-age-recent)';
    if (ageMs < 3_600_000) return 'var(--fws-age-aging)';
    if (ageMs < 21_600_000) return 'var(--fws-age-stale)';
    return 'var(--fws-age-dead)';
};

const fmtAgeDuration = (ms: number): string => {
    const s = Math.floor(ms / 1000);
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ${s % 60}s`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ${m % 60}m`;
    return `${Math.floor(h / 24)}d ${h % 24}h`;
};

const isoTimeOnly = (isoStr: string): string => isoStr.slice(11, 23);

interface EnrichedRow extends FwStateEntry {
    _ageMs: number;
    _health: 'ok' | 'expired' | 'oneway' | 'halfopen';
    _proto: number | undefined;
    _srcFlags: string[];
    _dstFlags: string[];
    _pktFwd: number;
    _pktBwd: number;
    _pktFwdExact: string;
    _pktBwdExact: string;
}

const enrichRow = (row: FwStateEntry): EnrichedRow => {
    const updatedNs = row.value?.updated_at;
    const nowMs = Date.now();
    let ageMs = 0;
    if (updatedNs) {
        try {
            const updatedMs = Number(BigInt(String(updatedNs)) / 1_000_000n);
            ageMs = Math.max(0, nowMs - updatedMs);
        } catch {
            ageMs = 0;
        }
    }
    const { source: srcFlags, destination: dstFlags } = decodeFlags(row.value?.flags);
    const pktFwd = normalizeUnsignedIntToNumber(row.value?.packets_forward);
    const pktBwd = normalizeUnsignedIntToNumber(row.value?.packets_backward);
    const pktFwdExact = normalizeUnsignedInt(row.value?.packets_forward) ?? '0';
    const pktBwdExact = normalizeUnsignedInt(row.value?.packets_backward) ?? '0';
    const proto = row.key?.proto;

    let health: EnrichedRow['_health'] = 'ok';
    if (row.expired) {
        health = 'expired';
    } else if (proto === 6 && srcFlags.includes('SYN') && !srcFlags.includes('ACK') && dstFlags.length === 0) {
        health = 'halfopen';
    } else if (pktBwd === 0 && (proto === 6 || proto === 17)) {
        health = 'oneway';
    }

    return {
        ...row,
        _ageMs: ageMs,
        _health: health,
        _proto: proto,
        _srcFlags: srcFlags,
        _dstFlags: dstFlags,
        _pktFwd: pktFwd,
        _pktBwd: pktBwd,
        _pktFwdExact: pktFwdExact,
        _pktBwdExact: pktBwdExact,
    };
};

const ANOMALY_PRESETS = [
    { id: 'all', label: 'All', health: null as null | string, color: 'var(--fws-text-3)' },
    { id: 'expired', label: 'Expired', health: 'expired', color: 'var(--fws-red)' },
    { id: 'oneway', label: 'One-way', health: 'oneway', color: 'var(--fws-amber)' },
    { id: 'halfopen', label: 'Half-open', health: 'halfopen', color: 'var(--fws-blue)' },
];

interface DistStats {
    sample: number;
    proto: Record<string, number>;
    health: Record<string, number>;
    age: number[];
}

const computeDistStats = (rows: EnrichedRow[]): DistStats => {
    const proto: Record<string, number> = { TCP: 0, UDP: 0, ICMP: 0, OTHER: 0 };
    const health: Record<string, number> = { ok: 0, oneway: 0, halfopen: 0, expired: 0 };
    const age = [0, 0, 0, 0, 0, 0];

    for (const s of rows) {
        const p = s._proto;
        if (p === 6) proto.TCP++;
        else if (p === 17) proto.UDP++;
        else if (p === 1 || p === 58) proto.ICMP++;
        else proto.OTHER++;

        health[s._health] = (health[s._health] ?? 0) + 1;

        const a = s._ageMs;
        if (a < 10_000) age[0]++;
        else if (a < 30_000) age[1]++;
        else if (a < 300_000) age[2]++;
        else if (a < 3_600_000) age[3]++;
        else if (a < 21_600_000) age[4]++;
        else age[5]++;
    }

    return { sample: rows.length, proto, health, age };
};

const StatusDot: React.FC<{ health: EnrichedRow['_health'] }> = ({ health }) => {
    const titles: Record<string, string> = {
        ok: 'Established',
        oneway: 'One-way — no backward packets',
        halfopen: 'Half-open — SYN, no ACK',
        expired: 'Expired',
    };
    return <span className={`fws-sdot fws-sdot--${health}`} title={titles[health]} />;
};

const FLAT_COLS = ['', 'IDX', 'SOURCE', 'DESTINATION', 'PROTO', 'SRC FLAGS', 'DST FLAGS', 'ORIGIN', 'PKT →', 'PKT ←', 'AGE', 'UPDATED'];
const FLAT_COL_WIDTHS = [30, 52, 280, 280, 68, 108, 108, 80, 110, 110, 90, 148];
const FLAT_COL_ALIGNS = ['', '', '', '', 'center', '', '', '', 'right', 'right', '', ''];
const FWSTATE_STATES_TOTAL_WIDTH = FLAT_COL_WIDTHS.reduce((a, b) => a + b, 0);

const colCellStyle = (colIdx: number): React.CSSProperties => ({
    width: FLAT_COL_WIDTHS[colIdx],
    minWidth: FLAT_COL_WIDTHS[colIdx],
    flexShrink: 0,
    overflow: 'hidden',
    paddingLeft: colIdx === 0 ? 14 : 8,
    paddingRight: 8,
    display: 'flex',
    alignItems: 'center',
    justifyContent: FLAT_COL_ALIGNS[colIdx] === 'center' ? 'center' : FLAT_COL_ALIGNS[colIdx] === 'right' ? 'flex-end' : 'flex-start',
    boxSizing: 'border-box',
});

interface FlatStateRowProps {
    row: EnrichedRow;
    start: number;
    isExpired: boolean;
}

const FlatStateRow: React.FC<FlatStateRowProps> = ({ row, start, isExpired }) => {
    const updatedIso = formatNsUtc(row.value?.updated_at);
    return (
        <div
            className={`fws-strow${isExpired ? ' fws-strow--expired' : ''}`}
            style={{
                position: 'absolute',
                top: start,
                left: 0,
                height: STATES_TABLE_ROW_HEIGHT,
                minWidth: FWSTATE_STATES_TOTAL_WIDTH,
                width: '100%',
                display: 'flex',
                alignItems: 'center',
                borderBottom: '1px solid var(--fws-border-soft)',
            }}
        >
            <div style={colCellStyle(0)}><StatusDot health={row._health} /></div>
            <div style={{ ...colCellStyle(1), color: 'var(--fws-text-3)', fontFamily: 'var(--fws-mono)', fontSize: 11.5 }}>{formatStateIdx(row.idx)}</div>
            <div style={colCellStyle(2)}><span className="fws-pill fws-pill--src">{ipAddressToString(row.key?.src_addr as IPAddressWire | undefined) || '—'}</span></div>
            <div style={colCellStyle(3)}><span className="fws-pill fws-pill--dst">{ipAddressToString(row.key?.dst_addr as IPAddressWire | undefined) || '—'}</span></div>
            <div style={colCellStyle(4)}><span className={protoClass(row._proto)}>{protoLabel(row._proto)}</span></div>
            <div style={colCellStyle(5)}>{renderFlagChips(row._srcFlags)}</div>
            <div style={colCellStyle(6)}>{renderFlagChips(row._dstFlags)}</div>
            <div style={colCellStyle(7)}>
                {row.value?.external
                    ? <span className="fws-badge fws-badge--dim">sync</span>
                    : <span className="fws-badge fws-badge--green">local</span>}
            </div>
            <div style={colCellStyle(8)}>
                <span className="fws-pktcell" title={row._pktFwdExact}>
                    <span className="fws-arrow">→</span>{fmtCompact(row._pktFwd)}
                </span>
            </div>
            <div style={colCellStyle(9)}>
                <span
                    className={`fws-pktcell${row._pktBwd === 0 ? ' fws-pktcell--zero' : ''}`}
                    title={row._pktBwdExact}
                >
                    <span className="fws-arrow">←</span>{fmtCompact(row._pktBwd)}
                </span>
            </div>
            <div style={colCellStyle(10)}>
                <span className="fws-agecell">
                    <span className="fws-agedot" style={{ background: ageColorStyle(row._ageMs) }} />
                    {fmtAgeDuration(row._ageMs)}
                </span>
            </div>
            <div style={colCellStyle(11)}>
                <span className="fws-mono fws-updated" title={updatedIso !== '-' ? updatedIso : undefined}>
                    {updatedIso !== '-' ? isoTimeOnly(updatedIso) : '—'}
                </span>
            </div>
        </div>
    );
};

interface DistributionStripProps {
    dist: DistStats;
    mapTotal: number;
    collapsed: boolean;
    onToggle: () => void;
}

const DistributionStrip: React.FC<DistributionStripProps> = ({ dist, mapTotal, collapsed, onToggle }) => {
    const protoEntries = [
        { key: 'TCP', color: 'var(--fws-tcp)' },
        { key: 'UDP', color: 'var(--fws-udp)' },
        { key: 'ICMP', color: 'var(--fws-icmp)' },
        { key: 'OTHER', color: 'var(--fws-other)' },
    ];
    const sum = Math.max(1, dist.sample);
    const ageColors = [
        'var(--fws-age-fresh)', 'var(--fws-age-fresh)', 'var(--fws-age-recent)',
        'var(--fws-age-aging)', 'var(--fws-age-stale)', 'var(--fws-age-dead)',
    ];
    const ageLabels = ['<10s', '<30s', '<5m', '<1h', '<6h', '6h+'];
    const ageMax = Math.max(1, ...dist.age);

    if (collapsed) {
        return (
            <div className="fws-distrib fws-distrib--collapsed">
                <button className="fws-dcollapse fws-dcollapse--full" onClick={onToggle} title="Show overview" aria-label="Show distribution overview">
                    <span className="fws-dcollapse-label">Overview</span>
                    <span className="fws-chevron fws-chevron--up" />
                </button>
            </div>
        );
    }

    return (
        <div className="fws-distrib">
            <div className="fws-dwrap">
                <div className="fws-dblock fws-dtotal">
                    <span className="fws-dh">States in map</span>
                    <div><span className="fws-big">{fmtCompact(mapTotal)}</span></div>
                </div>

                <div className="fws-dvrule" />

                <div className="fws-dblock" style={{ flex: 1, minWidth: 240 }}>
                    <span className="fws-dh">
                        Protocol mix <em className="fws-sample">— sample of {fmtInt(dist.sample)} loaded</em>
                    </span>
                    <div className="fws-stackbar">
                        {protoEntries.map((p) => (
                            <span key={p.key} style={{ width: `${(dist.proto[p.key] / sum) * 100}%`, background: p.color }} />
                        ))}
                    </div>
                    <div className="fws-stacklegend">
                        {protoEntries.map((p) => (
                            <span key={p.key} className="fws-li">
                                <span className="fws-sw" style={{ background: p.color }} />
                                {p.key} <b>{((dist.proto[p.key] / sum) * 100).toFixed(p.key === 'TCP' || p.key === 'UDP' ? 0 : 1)}%</b>
                            </span>
                        ))}
                    </div>
                </div>

                <div className="fws-dblock">
                    <span className="fws-dh">Health <em className="fws-sample">— in loaded</em></span>
                    <div className="fws-stacklegend" style={{ marginTop: 2 }}>
                        <span className="fws-li"><span className="fws-sdot fws-sdot--ok" /> OK <b>{fmtInt(dist.health.ok ?? 0)}</b></span>
                        <span className="fws-li"><span className="fws-sdot fws-sdot--oneway" /> One-way <b>{fmtInt(dist.health.oneway ?? 0)}</b></span>
                        <span className="fws-li"><span className="fws-sdot fws-sdot--halfopen" /> Half-open <b>{fmtInt(dist.health.halfopen ?? 0)}</b></span>
                        <span className="fws-li"><span className="fws-sdot fws-sdot--expired" /> Expired <b>{fmtInt(dist.health.expired ?? 0)}</b></span>
                    </div>
                </div>

                <div className="fws-dblock">
                    <span className="fws-dh">Age</span>
                    <div className="fws-minibars">
                        {dist.age.map((v, idx) => (
                            <div key={idx} title={`${ageLabels[idx]} · ${fmtInt(v)}`}
                                className="fws-mb" style={{ height: 6 + (v / ageMax) * 28, background: ageColors[idx] }} />
                        ))}
                    </div>
                </div>
            </div>
            <button className="fws-dcollapse" onClick={onToggle} title="Hide overview" aria-label="Hide distribution overview">
                <span className="fws-chevron" />
            </button>
        </div>
    );
};

interface StatesTabBodyProps {
    currentName: string;
    mapName: string;
    statesQuery: StatesQuery;
    updateStatesQuery: (q: StatesQuery) => void;
    canLoadStates: boolean;
    stats: { ipv4?: MapStats; ipv6?: MapStats } | null;
}

const StatesTabBody: React.FC<StatesTabBodyProps> = ({
    currentName,
    mapName,
    statesQuery,
    updateStatesQuery,
    canLoadStates,
    stats,
}) => {
    const [preset, setPreset] = useState<string>('all');
    const [distCollapsed, setDistCollapsed] = useState(false);

    const [rows, setRows] = useState<EnrichedRow[]>([]);
    const [stateLoading, setStateLoading] = useState(false);
    const [stateHasMore, setStateHasMore] = useState(true);

    const rowsRef = useRef<EnrichedRow[]>([]);
    const stateLoadingRef = useRef(false);
    const stateHasMoreRef = useRef(true);
    const stateCursorRef = useRef(0);
    const stateGenerationRef = useRef<string | null>(null);
    const abortRef = useRef<AbortController | null>(null);
    const requestIdRef = useRef(0);
    const inFlightKeyRef = useRef<string | null>(null);
    const lastLoadedKeyRef = useRef<string | null>(null);

    const scrollRef = useRef<HTMLDivElement | null>(null);
    const headerInnerRef = useRef<HTMLDivElement | null>(null);

    const bodyHeight = useContainerHeight(scrollRef as React.RefObject<HTMLElement | null>, 300, STATES_CURSORBAR_HEIGHT);

    const queryKey = useMemo(() => JSON.stringify({
        currentName,
        mapName,
        layerIndex: statesQuery.layerIndex,
        direction: statesQuery.direction,
        includeExpired: statesQuery.includeExpired,
    }), [currentName, mapName, statesQuery]);

    const mapTotal = statesQuery.isIpv6
        ? normalizeUnsignedIntToNumber(stats?.ipv6?.total_elements)
        : normalizeUnsignedIntToNumber(stats?.ipv4?.total_elements);

    const resetView = useCallback((clearLoading = false): void => {
        abortRef.current?.abort();
        abortRef.current = null;
        requestIdRef.current += 1;
        inFlightKeyRef.current = null;
        stateGenerationRef.current = null;
        setRows([]);
        rowsRef.current = [];
        stateCursorRef.current = 0;
        setStateHasMore(true);
        stateHasMoreRef.current = true;
        if (clearLoading) {
            setStateLoading(false);
            stateLoadingRef.current = false;
        }
        if (scrollRef.current) {
            scrollRef.current.scrollTop = 0;
        }
    }, []);

    const loadPage = useCallback(async (reset: boolean): Promise<void> => {
        if (!canLoadStates || !currentName || !mapName) return;
        if (stateLoadingRef.current) return;
        if (!reset && !stateHasMoreRef.current) return;

        abortRef.current?.abort();
        const abort = new AbortController();
        abortRef.current = abort;
        const requestId = ++requestIdRef.current;

        setStateLoading(true);
        stateLoadingRef.current = true;

        // The selected family's standalone fwstate-map object owns the
        // table; entries are read from the map service by name.
        const request: ListEntriesRequest = {
            map_name: mapName,
            layer_index: statesQuery.layerIndex,
            include_expired: statesQuery.includeExpired,
            direction: statesQuery.direction,
            batch_size: STATES_TABLE_MAX_BATCH_SIZE,
            index: reset
                ? (statesQuery.direction === Direction.BACKWARD ? BACKWARD_RESET_CURSOR : 0)
                : stateCursorRef.current,
        };

        let shouldMarkLoaded = true;
        try {
            const res = await API.fwstatemap.listEntriesPage(request, { signal: abort.signal });
            if (requestIdRef.current !== requestId) return;
            const generation = normalizeUnsignedInt(res.generation) ?? '0';
            if (stateGenerationRef.current !== null && generation !== stateGenerationRef.current) {
                shouldMarkLoaded = false;
                resetView(true);
                lastLoadedKeyRef.current = null;
                inFlightKeyRef.current = null;
                toaster.warning('fwstate-generation', 'State generation changed. Reload from start.');
            } else {
                stateGenerationRef.current = generation;
                const newEntries = (res.entries ?? []).map(enrichRow);
                const nextRows = reset ? newEntries : [...rowsRef.current, ...newEntries];
                const nextCursor = normalizeUnsignedIntToNumber(res.index);
                const nextHasMore = Boolean(res.has_more);
                setRows(nextRows);
                rowsRef.current = nextRows;
                stateCursorRef.current = nextCursor;
                setStateHasMore(nextHasMore);
                stateHasMoreRef.current = nextHasMore;
            }
        } catch (err) {
            if (requestIdRef.current === requestId) {
                toaster.error('fwstate-entries', 'Failed to load fwstate-map entries', err);
            }
        }

        if (requestIdRef.current === requestId) {
            abortRef.current = null;
            if (shouldMarkLoaded) {
                lastLoadedKeyRef.current = queryKey;
            }
            inFlightKeyRef.current = null;
            setStateLoading(false);
            stateLoadingRef.current = false;
        }
    }, [canLoadStates, currentName, mapName, queryKey, resetView, statesQuery]);

    useEffect(() => {
        resetView(true);
    }, [resetView, queryKey]);

    useEffect(() => {
        return () => { abortRef.current?.abort(); };
    }, []);

    useEffect(() => {
        if (!canLoadStates || !currentName || !mapName || stateLoadingRef.current) return;
        if (lastLoadedKeyRef.current === queryKey) return;
        if (inFlightKeyRef.current === queryKey) return;
        inFlightKeyRef.current = queryKey;
        void loadPage(true);
    }, [canLoadStates, currentName, loadPage, queryKey]);

    useEffect(() => {
        if (!canLoadStates) {
            resetView(true);
        }
    }, [canLoadStates, resetView]);

    const pulledCount = rows.length;

    const dist = useMemo(() => computeDistStats(rows), [rows]);

    const presetHealth = ANOMALY_PRESETS.find((p) => p.id === preset)?.health ?? null;

    const displayRows = useMemo(() => {
        if (!presetHealth) return rows;
        return rows.filter((r) => r._health === presetHealth);
    }, [rows, presetHealth]);

    const rowVirtualizer = useVirtualizer({
        count: displayRows.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => STATES_TABLE_ROW_HEIGHT,
        overscan: STATES_TABLE_OVERSCAN,
    });

    const virtualRows = rowVirtualizer.getVirtualItems();

    useEffect(() => {
        const lastItem = virtualRows[virtualRows.length - 1];
        if (!lastItem) return;
        if (!stateHasMoreRef.current || stateLoadingRef.current) return;
        if (lastLoadedKeyRef.current !== queryKey) return;
        if (lastItem.index >= displayRows.length - STATES_TABLE_LOAD_THRESHOLD) {
            void loadPage(false);
        }
    }, [virtualRows, displayRows.length, loadPage, queryKey]);

    useEffect(() => {
        const el = scrollRef.current;
        if (!el) return;
        const onScroll = (): void => {
            const inner = headerInnerRef.current;
            if (inner) {
                inner.style.transform = `translateX(-${el.scrollLeft}px)`;
            }
        };
        el.addEventListener('scroll', onScroll, { passive: true });
        return () => el.removeEventListener('scroll', onScroll);
    }, []);

    const handleRefreshTable = useCallback((): void => {
        lastLoadedKeyRef.current = null;
        inFlightKeyRef.current = null;
        resetView(true);
        void loadPage(true);
    }, [loadPage, resetView]);

    return (
        <div className="fws-states">
            <div className="fws-queryrow">
                <div className="fws-qfield">
                    <span className="fws-field-label">Address family</span>
                    <SegmentedRadioGroup
                        size="m"
                        value={statesQuery.isIpv6 ? 'ipv6' : 'ipv4'}
                        onUpdate={(v) => updateStatesQuery({ ...statesQuery, isIpv6: v === 'ipv6' })}
                    >
                        <SegmentedRadioGroup.Option value="ipv6" content="IPv6" />
                        <SegmentedRadioGroup.Option value="ipv4" content="IPv4" />
                    </SegmentedRadioGroup>
                </div>
                <div className="fws-qfield">
                    <span className="fws-field-label">Direction</span>
                    <Select
                        size="m"
                        value={[String(statesQuery.direction)]}
                        onUpdate={(v) => updateStatesQuery({ ...statesQuery, direction: Number(v[0] ?? 0) as Direction })}
                        options={[
                            { value: String(Direction.FORWARD), content: 'Forward' },
                            { value: String(Direction.BACKWARD), content: 'Backward' },
                        ]}
                    />
                </div>
                <div className="fws-qfield" style={{ width: 100 }}>
                    <span className="fws-field-label">State layer</span>
                    <TextInput
                        size="m"
                        type="number"
                        value={String(statesQuery.layerIndex)}
                        onUpdate={(v) => updateStatesQuery({ ...statesQuery, layerIndex: normalizeUnsignedIntToNumber(v) })}
                    />
                </div>
                <div className="fws-qfield">
                    <span className="fws-field-label">Include expired</span>
                    <div className="fws-switch-row">
                        <Switch
                            checked={statesQuery.includeExpired}
                            onUpdate={(v) => updateStatesQuery({ ...statesQuery, includeExpired: v })}
                        />
                    </div>
                </div>
            </div>

            <div className="fws-viewbar">
                <span className="fws-vb-label">Highlight in loaded rows</span>
                <div className="fws-presets">
                    {ANOMALY_PRESETS.map((p) => {
                        const cnt = p.health ? (dist.health[p.health] ?? 0) : dist.sample;
                        return (
                            <button
                                key={p.id}
                                className={`fws-preset-btn${preset === p.id ? ' fws-preset-btn--on' : ''}`}
                                onClick={() => setPreset(p.id)}
                            >
                                <span className="fws-dot" style={{ background: p.color }} />
                                {p.label}
                                <span className="fws-n">{fmtInt(cnt)}</span>
                            </button>
                        );
                    })}
                </div>
                <div style={{ flex: 1 }} />
                <span className="fws-scope-note" title="No server-side filter API: presets and highlights only scan the rows already pulled through the cursor.">
                    <svg className="fws-ic" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <circle cx="12" cy="12" r="9"/><path d="M12 16v-4M12 8h.01"/>
                    </svg>
                    filters scan loaded rows only
                </span>
            </div>

            <DistributionStrip
                dist={dist}
                mapTotal={mapTotal}
                collapsed={distCollapsed}
                onToggle={() => setDistCollapsed((v) => !v)}
            />

            <div className="fws-tablezone">
                <div className="fws-tblshell">
                    <div className="fws-tblheader">
                        <div
                            ref={headerInnerRef}
                            style={{ display: 'flex', minWidth: FWSTATE_STATES_TOTAL_WIDTH, height: '100%', alignItems: 'center', willChange: 'transform' }}
                        >
                            {FLAT_COLS.map((col, colIdx) => (
                                <div
                                    key={`th-${colIdx}`}
                                    style={{
                                        width: FLAT_COL_WIDTHS[colIdx],
                                        minWidth: FLAT_COL_WIDTHS[colIdx],
                                        flexShrink: 0,
                                        textAlign: (FLAT_COL_ALIGNS[colIdx] as React.CSSProperties['textAlign']) || 'left',
                                        paddingLeft: colIdx === 0 ? 14 : 8,
                                        paddingRight: 8,
                                        boxSizing: 'border-box',
                                    }}
                                    className="fws-th"
                                >
                                    {col}
                                </div>
                            ))}
                        </div>
                    </div>

                    <div
                        className="fws-tablescroll"
                        ref={scrollRef}
                        style={{ flex: '0 0 auto', height: bodyHeight, overflowY: 'auto' }}
                    >
                        {displayRows.length === 0 && !stateLoading && (
                            <div className="fws-tableempty">
                                {canLoadStates
                                    ? preset !== 'all'
                                        ? (
                                            <>
                                                <div>No <b>{ANOMALY_PRESETS.find((p) => p.id === preset)?.label}</b> states in {fmtInt(pulledCount)} loaded rows.</div>
                                                <div style={{ display: 'flex', gap: 8 }}>
                                                    <Button size="s" onClick={() => setPreset('all')}>Show all</Button>
                                                    {stateHasMore && (
                                                        <Button size="s" view="outlined" onClick={() => { void loadPage(false); }}>Pull more rows</Button>
                                                    )}
                                                </div>
                                            </>
                                        )
                                        : <div>{pulledCount === 0 ? 'Loading states…' : 'No states found.'}</div>
                                    : <div>This FWState config names no linked map for the selected family — states are not available.</div>
                                }
                            </div>
                        )}

                        {displayRows.length > 0 && (
                            <div
                                style={{
                                    height: rowVirtualizer.getTotalSize(),
                                    minWidth: FWSTATE_STATES_TOTAL_WIDTH,
                                    position: 'relative',
                                }}
                            >
                                {virtualRows.map((virtualRow) => {
                                    const row = displayRows[virtualRow.index];
                                    if (!row) return null;
                                    return (
                                        <FlatStateRow
                                            key={String(row.idx)}
                                            row={row}
                                            start={virtualRow.start}
                                            isExpired={Boolean(row.expired)}
                                        />
                                    );
                                })}
                            </div>
                        )}
                    </div>
                </div>
            </div>

            <div className="fws-cursorbar">
                <div className="fws-pulled">
                    <span>Pulled <b>{fmtInt(pulledCount)}</b> states</span>
                    <span className="fws-hint">of</span>
                    <span>≈ <b>{fmtCompact(mapTotal || 0)}</b> in map</span>
                    {presetHealth && (
                        <span className="fws-showing">
                            · showing <b>{fmtInt(displayRows.length)}</b> {ANOMALY_PRESETS.find((p) => p.id === preset)?.label?.toLowerCase()}
                        </span>
                    )}
                </div>
                {stateHasMore ? (
                    <Button
                        size="s"
                        loading={stateLoading}
                        onClick={() => { void loadPage(false); }}
                    >
                        Pull more
                    </Button>
                ) : (
                    <span className="fws-endcap">— cursor exhausted —</span>
                )}
                <div style={{ flex: 1 }} />
                <button
                    className="fws-preset-btn"
                    title="Refresh table"
                    aria-label="Refresh table"
                    onClick={handleRefreshTable}
                >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M21 12a9 9 0 1 1-3-6.7L21 8M21 3v5h-5"/>
                    </svg>
                </button>
            </div>
        </div>
    );
};

interface MapNameFieldProps {
    label: string;
    kind: MapKind;
    inputId: string;
    datalistId: string;
    value: string;
    /** Names of this field's family only, feeding the suggestions. */
    familyMapNames: string[];
    /** Every known map's family keyed by name, scoping existence checks. */
    mapKinds: Record<string, MapKind>;
    busy: boolean;
    requiredError: string;
    placeholder: string;
    onUpdate: (value: string) => void;
    onCreate: (name: string, kind: MapKind) => void;
    onDeleteRequest: (name: string, kind: MapKind) => void;
}

// One family's map-name field: free-text input with this family's map
// names as datalist suggestions, plus the create and delete affordances.
// Existence is scoped to the field's family: a name registered for the
// other family neither enables delete here nor allows creating a same
// name (the registry's namespace spans both families).
const MapNameField: React.FC<MapNameFieldProps> = ({
    label,
    kind,
    inputId,
    datalistId,
    value,
    familyMapNames,
    mapKinds,
    busy,
    requiredError,
    placeholder,
    onUpdate,
    onCreate,
    onDeleteRequest,
}) => {
    const trimmed = value.trim();
    const knownKind = trimmed !== '' ? mapKinds[trimmed] : undefined;
    const exists = knownKind === kind;
    const existsOtherFamily = knownKind !== undefined && knownKind !== kind;
    const familyLabel = kind === MapKind.V4 ? 'IPv6' : 'IPv4';
    const createTitle = trimmed === ''
        ? 'Type a map name to create it'
        : existsOtherFamily
            ? `Name "${trimmed}" is already used by a ${familyLabel} map`
            : exists
                ? `Map "${trimmed}" already exists`
                : `Create map "${trimmed}" with default sizing`;
    const deleteTitle = trimmed === ''
        ? 'Enter the name of an existing map'
        : existsOtherFamily
            ? `Map "${trimmed}" belongs to the ${familyLabel} family; manage it beside that family's field`
            : exists
                ? `Delete map "${trimmed}"`
                : `No map named "${trimmed}" exists`;

    return (
        <div className="fwstate-map-field">
            <label className="fwstate-map-field__label" htmlFor={inputId}>
                <Text variant="caption-2" color="secondary">{label}</Text>
            </label>
            <div className="fwstate-map-field__row">
                <div className="fwstate-map-field__input">
                    <TextInput
                        id={inputId}
                        controlProps={{ list: datalistId }}
                        value={value}
                        onUpdate={onUpdate}
                        error={!value.trim() ? requiredError : undefined}
                        placeholder={placeholder}
                    />
                    <datalist id={datalistId}>
                        {familyMapNames.map((name) => (
                            <option key={name} value={name} />
                        ))}
                    </datalist>
                </div>
                <div className="fwstate-map-field__actions">
                    <button
                        type="button"
                        className="yn-table-action-btn"
                        title={createTitle}
                        aria-label={createTitle}
                        disabled={busy || trimmed === '' || knownKind !== undefined}
                        onClick={() => onCreate(trimmed, kind)}
                    >
                        <Icon data={Plus} size={16} />
                    </button>
                    <button
                        type="button"
                        className="yn-table-action-btn yn-table-action-btn--delete"
                        title={deleteTitle}
                        aria-label={deleteTitle}
                        disabled={busy || !exists}
                        onClick={() => onDeleteRequest(trimmed, kind)}
                    >
                        <TrashIcon />
                    </button>
                </div>
            </div>
        </div>
    );
};

const FWStatePage: React.FC = () => {
    const navigate = useNavigate();
    const [searchParams, setSearchParams] = useSearchParams();
    const [loading, setLoading] = useState(true);
    const [activeSubTab, setActiveSubTab] = useState<StateSubTab>(() => getStateSubTab(searchParams));
    const [configs, setConfigs] = useState<Record<string, DraftConfig>>({});
    // Map names as published on the server, per config: live reads (stats,
    // states) follow the saved linkage, not an unsaved rename in the form.
    const [serverMapNames, setServerMapNames] = useState<Record<string, { v4: string; v6: string }>>({});
    const [dirtyConfigs, setDirtyConfigs] = useState<Set<string>>(new Set());
    const [stats, setStats] = useState<{ ipv4?: MapStats; ipv6?: MapStats } | null>(null);
    const [statsFailed, setStatsFailed] = useState(false);
    const [addConfigOpen, setAddConfigOpen] = useState(false);
    const [deleteConfigOpen, setDeleteConfigOpen] = useState(false);
    // Names of every standalone map the service knows, with each name's
    // family: the per-family views feed the two name fields' suggestions
    // and family-scoped existence checks.
    const [mapNames, setMapNames] = useState<string[]>([]);
    const [mapKinds, setMapKinds] = useState<Record<string, MapKind>>({});
    const [mapMutationBusy, setMapMutationBusy] = useState(false);
    const [deleteMapTarget, setDeleteMapTarget] = useState<{ name: string; kind: MapKind } | null>(null);

    const configsRef = useRef(configs);
    const dirtyConfigsRef = useRef(dirtyConfigs);
    const statsRequestIdRef = useRef(0);

    const configNames = useMemo(() => Object.keys(configs).sort((a, b) => compareNatural(a, b)), [configs]);
    const queryConfig = useMemo(() => searchParams.get(QP_CONFIG), [searchParams]);
    const currentName = useMemo(() => {
        if (queryConfig && (loading || configNames.includes(queryConfig))) {
            return queryConfig;
        }
        return configNames[0] || '';
    }, [configNames, queryConfig, loading]);
    const current = configs[currentName];
    const currentServerMapNames = serverMapNames[currentName];
    const canLoadStates = Boolean(currentName && current && !current.isLocalOnly);
    const currentIsDirty = dirtyConfigs.has(currentName);
    const anyDirty = dirtyConfigs.size > 0;

    const { updateParams } = useSearchParamHelpers(setSearchParams);

    const updateActiveConfig = useCallback((name: string): void => {
        updateParams({ [QP_CONFIG]: name || null });
    }, [updateParams]);

    useTabCycle({
        tabs: configNames,
        activeTab: currentName,
        onSelect: updateActiveConfig,
        enabled: !loading,
    });

    const updateActiveSubTab = useCallback((tab: StateSubTab): void => {
        updateParams({ [QP_TAB]: tab });
    }, [updateParams]);

    const statesQuery = useMemo(() => getStatesQuery(searchParams), [searchParams]);
    const updateStatesQuery = useCallback((query: StatesQuery): void => {
        updateParams(getStatesQueryParamValues(query));
    }, [updateParams]);
    const statesQueryParamUpdates = useMemo(() => getStatesQueryParamUpdates(searchParams, statesQuery), [searchParams, statesQuery]);

    useEffect(() => {
        const updates: Record<string, string | null> = { ...statesQueryParamUpdates };
        const activeTab = getStateSubTab(searchParams);
        if (activeSubTab !== activeTab) {
            setActiveSubTab(activeTab);
        }
        if (activeTab !== searchParams.get(QP_TAB)) {
            updates[QP_TAB] = activeTab;
        }
        if (!loading) {
            if (!currentName) {
                if (searchParams.get(QP_CONFIG) !== null) {
                    updates[QP_CONFIG] = null;
                }
            } else if (queryConfig !== currentName) {
                updates[QP_CONFIG] = currentName;
            }
        }
        if (Object.keys(updates).length > 0) {
            updateParams(updates);
        }
    }, [activeSubTab, configNames.length, currentName, loading, queryConfig, searchParams, statesQueryParamUpdates, updateParams]);

    useUnsavedChangesBlocker(anyDirty);

    useEffect(() => { configsRef.current = configs; }, [configs]);
    useEffect(() => { dirtyConfigsRef.current = dirtyConfigs; }, [dirtyConfigs]);

    const loadAll = useCallback(async (options?: { preserveDirty?: boolean; skipDirtyNames?: Set<string> }): Promise<void> => {
        setLoading(true);
        try {
            const [fwConfigsResp, inventoryNames] = await Promise.all([
                API.fwstate.listConfigs(),
                inventoryConfigNames('fwstate'),
            ]);
            const fwNames = unionConfigNames(fwConfigsResp.configs ?? [], inventoryNames);
            const fwFull = await loadKnownConfigs(
                fwNames,
                async (name) => ({ name, config: await API.fwstate.showConfig({ name }) }),
                { onDropped: warnConfigsUnknown('fwstate-configs-unknown', 'fwstate') },
            );
            const nextConfigs: Record<string, DraftConfig> = {};
            const nextServerMapNames: Record<string, { v4: string; v6: string }> = {};
            fwFull.forEach(({ name, config }) => {
                nextConfigs[name] = toDraftConfig(config, false);
                nextServerMapNames[name] = {
                    v4: config.map_name_v4 ?? '',
                    v6: config.map_name_v6 ?? '',
                };
            });
            // Replace the record wholesale so configs absent from this load drop out.
            setServerMapNames(nextServerMapNames);

            if (options?.preserveDirty) {
                const dirtySnapshot = dirtyConfigsRef.current;
                const configSnapshot = configsRef.current;
                const skipDirtyNames = options.skipDirtyNames ?? new Set<string>();
                const preservedDirtyNames = new Set(
                    Array.from(dirtySnapshot).filter((name) => !skipDirtyNames.has(name))
                );
                const mergedConfigs: Record<string, DraftConfig> = { ...nextConfigs };
                Object.entries(configSnapshot).forEach(([name, draft]) => {
                    if (preservedDirtyNames.has(name) || (draft.isLocalOnly && !nextConfigs[name] && !skipDirtyNames.has(name))) {
                        mergedConfigs[name] = draft;
                    }
                });
                setConfigs(mergedConfigs);
                setDirtyConfigs(new Set(Array.from(preservedDirtyNames).filter((name) => Boolean(mergedConfigs[name]))));
            } else {
                setConfigs(nextConfigs);
                setDirtyConfigs(new Set());
            }
        } catch (err) {
            toaster.error('fwstate-load', 'Failed to load FWState data', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void loadAll();
    }, [loadAll]);

    useEffect(() => {
        const requestId = ++statsRequestIdRef.current;
        setStats(null);
        setStatsFailed(false);
        // current is undefined while a deep link (?config=name) loads: the
        // name is known before the configs map fills in.
        if (!currentName || !current || current.isLocalOnly) return;
        // Both linked map objects own the state tables; read each family's
        // stats from the map service by published name.
        //
        // An empty name links no map for that family and stays silent; a
        // failed read is surfaced as a warning instead of passing for an
        // empty map.
        const readStats = (name: string): Promise<{ stats: MapStats | undefined; failed: boolean }> =>
            name
                ? API.fwstatemap.getMapStats({ name })
                    .then((res) => ({ stats: res.stats, failed: false }))
                    .catch(() => ({ stats: undefined, failed: true }))
                : Promise.resolve({ stats: undefined, failed: false });
        Promise.all([
            readStats(currentServerMapNames?.v4 ?? ''),
            readStats(currentServerMapNames?.v6 ?? ''),
        ]).then(([ipv4, ipv6]) => {
            if (statsRequestIdRef.current !== requestId) return;
            setStats({ ipv4: ipv4.stats, ipv6: ipv6.stats });
            setStatsFailed(ipv4.failed || ipv6.failed);
        });
    }, [currentName, current?.isLocalOnly, currentServerMapNames?.v4, currentServerMapNames?.v6]);

    const hasOtherDirtyConfigs = useCallback((name: string): boolean => {
        return Array.from(dirtyConfigs).some((dirtyName) => dirtyName !== name);
    }, [dirtyConfigs]);

    // Only this component's map-name state is replaced on refresh; config
    // drafts and dirty flags are never touched here.
    const refreshMapNames = useCallback(async (): Promise<void> => {
        try {
            const response = await API.fwstatemap.listMaps();
            setMapNames([...(response.maps ?? [])].sort(compareNatural));
            setMapKinds(response.kinds ?? {});
        } catch (err) {
            toaster.error('fwstate-map-list', 'Failed to load fwstate-map list', err);
        }
    }, []);

    // The name list feeds the configuration tab's suggestions and button
    // guards, so it loads on every entry into that tab, not on a timer.
    useEffect(() => {
        if (activeSubTab !== 'configuration') return;
        void refreshMapNames();
    }, [activeSubTab, refreshMapNames]);

    // Creating a map provisions the object only: the name field already
    // holds the value, so the config draft stays clean until saved apart.
    const handleCreateMap = async (name: string, kind: MapKind): Promise<void> => {
        setMapMutationBusy(true);
        try {
            await API.fwstatemap.createMap({ name, kind });
            toaster.success('fwstate-map-create', `Map "${name}" created with default sizing.`);
            await refreshMapNames();
        } catch (err) {
            toaster.error('fwstate-map-create', `Failed to create map "${name}"`, err);
        } finally {
            setMapMutationBusy(false);
        }
    };

    const requestDeleteMap = (name: string, kind: MapKind): void => {
        setDeleteMapTarget({ name, kind });
    };

    const handleDeleteMap = async (): Promise<void> => {
        const target = deleteMapTarget;
        if (!target || mapMutationBusy) return;
        setMapMutationBusy(true);
        try {
            await API.fwstatemap.deleteMap({ name: target.name });
            toaster.success('fwstate-map-delete', `Map "${target.name}" deleted.`);
            setDeleteMapTarget(null);
            await refreshMapNames();
        } catch (err) {
            // A refusal while a published module links the map arrives as
            // this error; the modal stays open so the toast can explain it.
            toaster.error('fwstate-map-delete', `Failed to delete map "${target.name}"`, err);
        } finally {
            setMapMutationBusy(false);
        }
    };

    const { configs: cachedConfigs, write: writeCache } = useConfigListCache('fwstate');

    // Cache config names so the config tab strip renders instantly on
    // remount instead of blanking while ListConfigs refetches.
    useEffect(() => {
        if (!loading && configNames.length > 0) {
            writeCache({
                configs: configNames,
                counts: {},
            });
        }
    }, [loading, configNames, writeCache]);

    const updateCurrent = (patch: Partial<DraftConfig>): void => {
        if (!currentName) return;
        setConfigs((prev) => ({ ...prev, [currentName]: { ...prev[currentName], ...patch } }));
        setDirtyConfigs((prev) => new Set(prev).add(currentName));
    };

    const validateCurrent = (): boolean => {
        if (!current) return false;
        const durationFields = [current.tcpSynAck, current.tcpSyn, current.tcpFin, current.tcp, current.udp, current.defaultTimeout];
        if (!current.mapNameV4.trim() || !current.mapNameV6.trim()) return false;
        if (current.portMulticast < 0 || current.portMulticast > 65535) return false;
        if (!isValidNonzeroIPv6Address(current.srcAddr)) return false;
        if (!isValidNonzeroIPv6Address(current.dstAddrMulticast) || current.portMulticast === 0) return false;
        if (durationFields.some((value) => parseDurationToNs(value) === null)) return false;
        return true;
    };

    const handleSave = async (): Promise<void> => {
        if (!current) return;
        if (hasOtherDirtyConfigs(currentName)) {
            toaster.error('fwstate-dirty-save', 'Save blocked: there are unsaved changes in other configs.');
            return;
        }
        if (!validateCurrent()) {
            toaster.error('fwstate-validate', 'Invalid FWState form fields');
            return;
        }
        const requestName = currentName;
        const syncConfig = {
            src_addr: stringToIPAddress(current.srcAddr),
            dst_addr_multicast: stringToIPAddress(current.dstAddrMulticast),
            port_multicast: current.portMulticast,
            tcp_syn_ack: parseDurationToNs(current.tcpSynAck) ?? undefined,
            tcp_syn: parseDurationToNs(current.tcpSyn) ?? undefined,
            tcp_fin: parseDurationToNs(current.tcpFin) ?? undefined,
            tcp: parseDurationToNs(current.tcp) ?? undefined,
            udp: parseDurationToNs(current.udp) ?? undefined,
            default: parseDurationToNs(current.defaultTimeout) ?? undefined,
        };
        try {
            await API.fwstate.updateConfig({
                name: requestName,
                map_name_v4: current.mapNameV4.trim(),
                map_name_v6: current.mapNameV6.trim(),
                sync_config: syncConfig,
            });
            toaster.success('fwstate-save', `Config "${requestName}" saved.`);
            setDirtyConfigs((prev) => {
                const next = new Set(prev);
                next.delete(currentName);
                return next;
            });
            await loadAll({ preserveDirty: true, skipDirtyNames: new Set([currentName]) });
        } catch (err) {
            toaster.error('fwstate-save-error', 'Failed to save FWState config', err);
        }
    };

    const handleDeleteConfig = async (): Promise<void> => {
        if (!currentName) return;
        if (hasOtherDirtyConfigs(currentName)) {
            toaster.error('fwstate-dirty-delete', 'Delete blocked: there are unsaved changes in other configs.');
            return;
        }
        if (current?.isLocalOnly) {
            setConfigs((prev) => {
                const next = { ...prev };
                delete next[currentName];
                return next;
            });
            setDirtyConfigs((prev) => {
                const next = new Set(prev);
                next.delete(currentName);
                return next;
            });
            const remainingNames = configNames.filter((name) => name !== currentName);
            updateActiveConfig(remainingNames[0] ?? '');
            setDeleteConfigOpen(false);
            return;
        }
        try {
            await API.fwstate.deleteConfig({ name: currentName });
            setDeleteConfigOpen(false);
            await loadAll({ preserveDirty: true, skipDirtyNames: new Set([currentName]) });
        } catch (err) {
            toaster.error('fwstate-delete-error', 'Failed to delete FWState config', err);
        }
    };

    const handleOpenAcl = useCallback((aclName?: string): void => {
        if (anyDirty && !window.confirm('You have unsaved changes. Leave this page anyway?')) {
            return;
        }
        navigate(aclName ? `/modules/acl?config=${encodeURIComponent(aclName)}` : '/modules/acl');
    }, [anyDirty, navigate]);

    const commands = useMemo((): Command[] => {
        const list: Command[] = [
            {
                id: '__add_config',
                icon: '▤',
                label: 'Add config',
                sub: 'Create a new FWState configuration',
                keywords: 'add config create new',
                onSelect: () => setAddConfigOpen(true),
            },
        ];
        if (currentIsDirty) {
            list.push({
                id: '__save',
                icon: '✓',
                label: 'Save config',
                sub: `Save "${currentName}"`,
                keywords: 'save commit apply',
                onSelect: () => { void handleSave(); },
            });
        }
        if (current) {
            list.push({
                id: '__delete_config',
                icon: '✕',
                label: 'Delete config',
                sub: `Delete "${currentName}"`,
                keywords: 'delete remove config',
                onSelect: () => setDeleteConfigOpen(true),
            });
        }
        for (const name of configNames) {
            if (name === currentName) continue;
            list.push({
                id: `__config_${name}`,
                icon: '⇥',
                label: `Switch to config ${name}`,
                sub: dirtyConfigs.has(name) ? 'unsaved changes' : undefined,
                keywords: `switch config tab ${name}`,
                onSelect: () => updateActiveConfig(name),
            });
        }
        for (const tab of STATE_SUB_TABS) {
            list.push({
                id: `__subtab_${tab.id}`,
                icon: '→',
                label: `Go to ${tab.label}`,
                keywords: `tab ${tab.id} ${tab.label}`,
                onSelect: () => updateActiveSubTab(tab.id),
            });
        }
        list.push({
            id: '__states_ipv4',
            icon: '4',
            label: 'States: filter IPv4',
            keywords: 'states filter ipv4 family',
            onSelect: () => updateStatesQuery({ ...statesQuery, isIpv6: false }),
        });
        list.push({
            id: '__states_ipv6',
            icon: '6',
            label: 'States: filter IPv6',
            keywords: 'states filter ipv6 family',
            onSelect: () => updateStatesQuery({ ...statesQuery, isIpv6: true }),
        });
        list.push({
            id: '__states_expired',
            icon: '⏱',
            label: 'States: toggle include-expired',
            sub: statesQuery.includeExpired ? 'Currently: on' : 'Currently: off',
            keywords: 'states expired toggle include',
            onSelect: () => updateStatesQuery({ ...statesQuery, includeExpired: !statesQuery.includeExpired }),
        });
        list.push({
            id: '__open_acl',
            icon: '↗',
            label: 'Open ACL module',
            keywords: 'acl module open navigate',
            onSelect: () => handleOpenAcl(),
        });
        return list;
    }, [
        current,
        currentName,
        currentIsDirty,
        configNames,
        dirtyConfigs,
        statesQuery,
        handleSave,
        updateActiveConfig,
        updateActiveSubTab,
        updateStatesQuery,
        handleOpenAcl,
    ]);

    const contribution = useMemo<PagePaletteContribution>(() => ({
        commands,
        placeholder: 'Search FWState actions…',
    }), [commands]);
    usePageContribution(contribution);

    const statsRows = useMemo(() => {
        const row = (label: string, getter: (s: MapStats | undefined) => string | number) => ({
            label,
            ipv4: getter(stats?.ipv4),
            ipv6: getter(stats?.ipv6),
        });
        return [
            row('Index slots', (s) => s?.index_size ?? '-'),
            row('Overflow buckets', (s) => s?.extra_bucket_count ?? '-'),
            row('Max chain', (s) => s?.max_chain_length ?? '-'),
            row('Layers', (s) => s?.layer_count ?? '-'),
            row('State entries', (s) => s?.total_elements ?? '-'),
            row('Max deadline', (s) => formatNsUtc(s?.max_deadline)),
            row('Memory used', (s) => formatMemoryBytes(s?.memory_used)),
        ];
    }, [stats]);

    const statsNote = stats?.ipv4?.note || stats?.ipv6?.note || '';

    const totalStatesV4 = normalizeUnsignedIntToNumber(stats?.ipv4?.total_elements);
    const totalStatesV6 = normalizeUnsignedIntToNumber(stats?.ipv6?.total_elements);
    const totalStates = totalStatesV4 + totalStatesV6;

    const subTabHeaderAction = activeSubTab === 'configuration' ? (
        <>
            <button
                type="button"
                className="yn-table-action-btn yn-table-action-btn--save"
                title="Save config"
                aria-label="Save config"
                disabled={!currentIsDirty}
                onClick={handleSave}
            >
                <SaveIcon />
            </button>
            <button
                type="button"
                className="yn-table-action-btn yn-table-action-btn--delete"
                title="Delete config"
                aria-label="Delete config"
                disabled={!current}
                onClick={() => setDeleteConfigOpen(true)}
            >
                <TrashIcon />
            </button>
        </>
    ) : null;

    const pageHeader = (
        <CommandPaletteHeader
            title="FWState"
            placeholder="Search FWState actions…"
            actions={<>
                <Button view="action" onClick={() => setAddConfigOpen(true)}>
                    <Icon data={Plus} size={16} />
                    Add Config
                </Button>
            </>}
        />
    );

    // While a warm cache exists, keep the config tab strip mounted from cached
    // names so it does not blink on remount; only the body reloads.
    const tabConfigs = loading ? cachedConfigs : configNames;

    if (loading && cachedConfigs.length === 0) {
        return <PageLayout header={pageHeader} className="yn-flat-layout"><PageLoader loading size="l" /></PageLayout>;
    }

    const configurationTab = current && (() => {
        const multicastAddrError = !isValidNonzeroIPv6Address(current.dstAddrMulticast) ? 'Non-zero IPv6 required' : undefined;
        const multicastPortError = current.portMulticast === 0 ? 'Port required' : current.portMulticast < 0 || current.portMulticast > 65535 ? '0..65535' : undefined;

        return (
            <div className="fwstate-config-panel">
                <div className="fwstate-settings-top-row">
                    <div className="fwstate-config-section">
                        <div className="fwstate-config-section__head">
                            <Text variant="subheader-2">State maps</Text>
                        </div>
                        <div className="fwstate-field-grid fwstate-field-grid--map">
                            <MapNameField
                                label="IPv4 map name"
                                kind={MapKind.V4}
                                inputId="fwstate-map-name-v4"
                                datalistId="fwstate-map-options-v4"
                                value={current.mapNameV4}
                                familyMapNames={mapNames.filter((name) => mapKinds[name] === MapKind.V4)}
                                mapKinds={mapKinds}
                                busy={mapMutationBusy}
                                requiredError="map_name_v4 is required"
                                placeholder="fwstate-map-v4"
                                onUpdate={(mapNameV4) => updateCurrent({ mapNameV4 })}
                                onCreate={handleCreateMap}
                                onDeleteRequest={requestDeleteMap}
                            />
                            <MapNameField
                                label="IPv6 map name"
                                kind={MapKind.V6}
                                inputId="fwstate-map-name-v6"
                                datalistId="fwstate-map-options-v6"
                                value={current.mapNameV6}
                                familyMapNames={mapNames.filter((name) => mapKinds[name] === MapKind.V6)}
                                mapKinds={mapKinds}
                                busy={mapMutationBusy}
                                requiredError="map_name_v6 is required"
                                placeholder="fwstate-map-v6"
                                onUpdate={(mapNameV6) => updateCurrent({ mapNameV6 })}
                                onCreate={handleCreateMap}
                                onDeleteRequest={requestDeleteMap}
                            />
                        </div>
                        <p className="fws-link-note">
                            The state tables live in standalone fwstate-map objects referenced here by name.
                            Maps can be provisioned from this page — type a new name and use the create
                            button next to the field, existing names appear as suggestions — or via{' '}
                            <code>yanet-cli-fwstatemap</code>. New maps are created with the service default
                            sizing; a map cannot be deleted while a published module config still links it.
                        </p>
                    </div>

                    <div className="fwstate-config-section">
                        <div className="fwstate-config-section__head">
                            <Text variant="subheader-2">Sync endpoints</Text>
                        </div>
                        <div className="fwstate-sync-grid">
                            <label className="fwstate-field fwstate-sync-grid__src">
                                <Text variant="caption-2" color="secondary">Sync source address</Text>
                                <TextInput value={current.srcAddr} onUpdate={(srcAddr) => updateCurrent({ srcAddr })} error={!isValidNonzeroIPv6Address(current.srcAddr) ? 'Non-zero IPv6 required' : undefined} placeholder="2001:db8::1" />
                            </label>
                            <div className="fwstate-sync-grid__endpoint">
                                <div className="fwstate-field">
                                    <Text variant="caption-2" color="secondary">Multicast endpoint</Text>
                                    <div className="fwstate-endpoint-row">
                                        <label className="fwstate-field">
                                            <Text variant="caption-2" color="secondary">Address</Text>
                                            <TextInput value={current.dstAddrMulticast} onUpdate={(dstAddrMulticast) => updateCurrent({ dstAddrMulticast })} error={multicastAddrError} placeholder="ff02::1" />
                                        </label>
                                        <label className="fwstate-field">
                                            <Text variant="caption-2" color="secondary">Port</Text>
                                            <TextInput type="number" value={String(current.portMulticast)} onUpdate={(v) => updateCurrent({ portMulticast: Number(v) })} error={multicastPortError} placeholder="2000" />
                                        </label>
                                    </div>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>

                <div className="fwstate-config-section">
                    <div className="fwstate-config-section__head">
                        <Text variant="subheader-2">Timeouts</Text>
                    </div>
                    <div className="fwstate-field-grid fwstate-field-grid--timeouts">
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">TCP SYN+ACK</Text>
                            <TextInput type="number" value={current.tcpSynAck} onUpdate={(tcpSynAck) => updateCurrent({ tcpSynAck })} error={parseDurationToNs(current.tcpSynAck) ? undefined : 'Enter seconds'} endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>} />
                        </label>
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">TCP SYN</Text>
                            <TextInput type="number" value={current.tcpSyn} onUpdate={(tcpSyn) => updateCurrent({ tcpSyn })} error={parseDurationToNs(current.tcpSyn) ? undefined : 'Enter seconds'} endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>} />
                        </label>
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">TCP FIN</Text>
                            <TextInput type="number" value={current.tcpFin} onUpdate={(tcpFin) => updateCurrent({ tcpFin })} error={parseDurationToNs(current.tcpFin) ? undefined : 'Enter seconds'} endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>} />
                        </label>
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">TCP established</Text>
                            <TextInput type="number" value={current.tcp} onUpdate={(tcp) => updateCurrent({ tcp })} error={parseDurationToNs(current.tcp) ? undefined : 'Enter seconds'} endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>} />
                        </label>
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">UDP</Text>
                            <TextInput type="number" value={current.udp} onUpdate={(udp) => updateCurrent({ udp })} error={parseDurationToNs(current.udp) ? undefined : 'Enter seconds'} endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>} />
                        </label>
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">Default</Text>
                            <TextInput type="number" value={current.defaultTimeout} onUpdate={(defaultTimeout) => updateCurrent({ defaultTimeout })} error={parseDurationToNs(current.defaultTimeout) ? undefined : 'Enter seconds'} endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>} />
                        </label>
                    </div>
                </div>
            </div>
        );
    })();

    const statisticsTab = current && (
        <section className="fws-stats-section">
            {statsFailed && (
                <Label theme="warning">Failed to read state-map statistics</Label>
            )}
            <div className="fws-statcards">
                <div className="fws-statcard">
                    <div className="fws-statcard__lbl">Total states</div>
                    <div className="fws-statcard__val">{fmtCompact(totalStates)}</div>
                    <div className="fws-statcard__meta">{fmtCompact(totalStatesV6)} v6 · {fmtCompact(totalStatesV4)} v4</div>
                </div>
                <div className="fws-statcard">
                    <div className="fws-statcard__lbl">Memory used</div>
                    <div className="fws-statcard__val">
                        {stats ? formatMemoryBytes((normalizeUnsignedIntToNumber(stats.ipv4?.memory_used) + normalizeUnsignedIntToNumber(stats.ipv6?.memory_used))) : '—'}
                    </div>
                    <div className="fws-statcard__meta">IPv4 + IPv6 maps</div>
                </div>
                <div className="fws-statcard">
                    <div className="fws-statcard__lbl">Max chain</div>
                    <div className="fws-statcard__val">
                        {stats ? `v4: ${stats.ipv4?.max_chain_length ?? '—'} · v6: ${stats.ipv6?.max_chain_length ?? '—'}` : '—'}
                    </div>
                    <div className="fws-statcard__meta">hash collision depth</div>
                </div>
                <div className="fws-statcard">
                    <div className="fws-statcard__lbl">Max deadline</div>
                    <div className="fws-statcard__val fws-statcard__val--mono">
                        {formatNsUtc(stats?.ipv6?.max_deadline ?? stats?.ipv4?.max_deadline)}
                    </div>
                    <div className="fws-statcard__meta">latest state expiry</div>
                </div>
            </div>

            <div className="fwstate-stats-compare">
                <div className="fwstate-stats-compare__head fwstate-stats-compare__head--metric">
                    <span>Metric</span>
                    {statsNote && (
                        <Tooltip content={statsNote} openDelay={0}>
                            <span className="fwstate-stats-compare__note-icon" aria-label={statsNote}>
                                <Icon data={CircleInfo} size={14} />
                            </span>
                        </Tooltip>
                    )}
                </div>
                <div className="fwstate-stats-compare__head">IPv4</div>
                <div className="fwstate-stats-compare__head">IPv6</div>
                {statsRows.map((row) => (
                    <React.Fragment key={row.label}>
                        <div className="fwstate-stats-compare__metric">{row.label}</div>
                        <div className="fwstate-stats-compare__value fwstate-mono">{row.ipv4}</div>
                        <div className="fwstate-stats-compare__value fwstate-mono">{row.ipv6}</div>
                    </React.Fragment>
                ))}
            </div>
        </section>
    );

    return (
        <PageLayout header={pageHeader} className="yn-flat-layout">
            <div className="yn-page yn-flat-page">
                {tabConfigs.length === 0 ? (
                    <EmptyPagePlaceholder
                        message="No FWState configurations found."
                        actionLabel="Add Config"
                        onAction={() => setAddConfigOpen(true)}
                    />
                ) : (
                    <>
                        <div className="fwstate-config-bar">
                            <div className="fwstate-config-bar__tabs">
                                <ConfigTabStrip
                                    configs={tabConfigs}
                                    activeConfig={currentName}
                                    dirtyConfigs={dirtyConfigs}
                                    onSelect={updateActiveConfig}
                                    onAddConfig={() => setAddConfigOpen(true)}
                                    addConfigDisabled={loading}
                                />
                            </div>
                        </div>

                        {loading ? (
                            <PageLoader loading size="l" />
                        ) : (
                        <div className="yn-content fwstate-content">
                            {current && (
                                <div className="fwstate-settings-layout">
                                    <div
                                        className={`fwstate-subtab-panel ${activeSubTab === 'states' ? 'fwstate-subtab-panel--states' : 'fwstate-subtab-panel--scroll'}`}
                                        role="tabpanel"
                                        id="fwstate-subtab-panel"
                                    >
                                        <div className="fwstate-subtab-frame">
                                            <div className="fwstate-subtab-frame__head">
                                                <div className="yn-tabs fwstate-sub-tabs" role="tablist" aria-label="FWState sub tabs">
                                                    {STATE_SUB_TABS.map((tab) => {
                                                        const isActive = tab.id === activeSubTab;
                                                        return (
                                                            <button
                                                                key={tab.id}
                                                                type="button"
                                                                role="tab"
                                                                aria-selected={isActive}
                                                                aria-controls="fwstate-subtab-panel"
                                                                className={`yn-tab${isActive ? ' yn-tab--active' : ''}`}
                                                                onClick={() => updateActiveSubTab(tab.id)}
                                                            >
                                                                <span className="yn-tab__label">{tab.label}</span>
                                                            </button>
                                                        );
                                                    })}
                                                </div>
                                                {subTabHeaderAction && <div className="fwstate-subtab-frame__actions">{subTabHeaderAction}</div>}
                                            </div>
                                        </div>
                                        {activeSubTab === 'configuration' && configurationTab}
                                        {activeSubTab === 'states' && (
                                            <StatesTabBody
                                                key={currentName}
                                                currentName={currentName}
                                                mapName={statesQuery.isIpv6 ? (currentServerMapNames?.v6 ?? '') : (currentServerMapNames?.v4 ?? '')}
                                                statesQuery={statesQuery}
                                                updateStatesQuery={updateStatesQuery}
                                                canLoadStates={canLoadStates}
                                                stats={stats}
                                            />
                                        )}
                                        {activeSubTab === 'statistics' && statisticsTab}
                                    </div>
                                </div>
                            )}
                        </div>
                        )}
                    </>
                )}
            </div>

            <AddConfigModal
                open={addConfigOpen}
                onClose={() => setAddConfigOpen(false)}
                placeholder="e.g. fwstate0"
                existingNames={configNames}
                onCreate={(name) => {
                    setConfigs((prev) => ({ ...prev, [name]: toDraftConfig(null, true) }));
                    setDirtyConfigs((prev) => new Set(prev).add(name));
                    updateActiveConfig(name);
                    setAddConfigOpen(false);
                }}
            />

            <DeleteConfigModal
                open={deleteConfigOpen}
                configName={currentName}
                onClose={() => setDeleteConfigOpen(false)}
                onConfirm={handleDeleteConfig}
            />

            <ConfirmModal
                open={deleteMapTarget !== null}
                title="Delete state map"
                confirmText="Delete"
                busy={mapMutationBusy}
                busyText="Deleting…"
                onClose={() => {
                    if (!mapMutationBusy) {
                        setDeleteMapTarget(null);
                    }
                }}
                onConfirm={() => { void handleDeleteMap(); }}
            >
                <p>Delete the state map <code>{deleteMapTarget?.name}</code>? This cannot be undone.</p>
                <p>Deletion is refused while a published module config still links this map.</p>
            </ConfirmModal>
        </PageLayout>
    );
};

export default FWStatePage;
