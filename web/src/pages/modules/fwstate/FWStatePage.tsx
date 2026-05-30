import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import {
    Button,
    Flex,
    Icon,
    Label,
    SegmentedRadioGroup,
    Select,
    Switch,
    Table,
    Text,
    TextInput,
    Tooltip,
} from '@gravity-ui/uikit';
import { CircleInfo, Plus } from '@gravity-ui/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { API } from '../../../api';
import { Direction, type FwStateEntry, type ListEntriesRequest, type MapStats } from '../../../api/fwstate';
import { ConfirmDialog, ConfigTabStrip, PageLayout, PageLoader } from '../../../components';
import { useContainerHeight } from '../../../hooks';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import { ipAddressToString, isValidIPAddress, parseIPToBytes, stringToIPAddress, type IPAddressWire } from '../../../utils/netip';
import { formatMACFromBytes, parseMACToBytes } from '../../../utils/mac';
import { formatBytes, toaster } from '../../../utils';
import { AddConfigModal, DeleteConfigModal } from '../../_shared/draft';
import { SaveIcon, TrashIcon } from '../../_shared/draft/DraftActionButtons';
import '../../../styles/draft-page.scss';
import './fwstate.scss';

interface DraftConfig {
    mapIndexSize: number;
    mapExtraBucketCount: number;
    srcAddr: string;
    dstEther: string;
    dstAddrMulticast: string;
    portMulticast: number;
    dstAddrUnicast: string;
    portUnicast: number;
    syncMode: 'multicast' | 'unicast' | 'both';
    tcpSynAck: string;
    tcpSyn: string;
    tcpFin: string;
    tcp: string;
    udp: string;
    defaultTimeout: string;
    linkedAcls: string[];
    isLocalOnly: boolean;
}

interface AclMeta {
    name: string;
    fwstateName: string;
    ruleCount: number | null;
    isLoaded: boolean;
    loadFailed: boolean;
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
const STATES_TABLE_HEADER_HEIGHT = 38;
const STATES_TABLE_BOTTOM_OFFSET = 86;
const STATES_TABLE_OVERSCAN = 12;
const STATES_TABLE_LOAD_THRESHOLD = 60;
const STATES_TABLE_MAX_BATCH_SIZE = 10000;
const BACKWARD_RESET_CURSOR = Number.MAX_SAFE_INTEGER;

const zeroIPv6AddressWire = (): IPAddressWire => ({ addr: '::' });

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

const isValidNonzeroMAC = (value: string): boolean => {
    const parsed = parseMACToBytes(value);
    return Boolean(parsed && parsed.some((byte) => byte !== 0));
};

const decodeWireBytes = (wire: string | Uint8Array | number[] | undefined): number[] => {
    if (!wire) return [];
    if (Array.isArray(wire)) return wire;
    if (wire instanceof Uint8Array) return Array.from(wire);
    try {
        return Array.from(atob(wire), (char) => char.charCodeAt(0));
    } catch {
        return [];
    }
};

const toDraftConfig = (config: Awaited<ReturnType<typeof API.fwstate.showConfig>> | null, isLocalOnly: boolean): DraftConfig => {
    const sync = config?.sync_config;
    const multicastAddress = ipAddressToString(sync?.dst_addr_multicast as IPAddressWire | undefined).trim();
    const unicastAddress = ipAddressToString(sync?.dst_addr_unicast as IPAddressWire | undefined).trim();
    const multicastPresent = isValidNonzeroIPv6Address(multicastAddress) && (sync?.port_multicast ?? 0) !== 0;
    const unicastPresent = isValidNonzeroIPv6Address(unicastAddress) && (sync?.port_unicast ?? 0) !== 0;
    const syncMode: DraftConfig['syncMode'] = multicastPresent && unicastPresent
        ? 'both'
        : unicastPresent
            ? 'unicast'
            : 'multicast';
    return {
        mapIndexSize: config?.map_config?.index_size ?? 1_048_576,
        mapExtraBucketCount: config?.map_config?.extra_bucket_count ?? 1_024,
        srcAddr: ipAddressToString(sync?.src_addr as IPAddressWire | undefined),
        dstEther: formatMACFromBytes(decodeWireBytes(sync?.dst_ether)),
        dstAddrMulticast: ipAddressToString(sync?.dst_addr_multicast as IPAddressWire | undefined),
        portMulticast: sync?.port_multicast ?? 0,
        dstAddrUnicast: ipAddressToString(sync?.dst_addr_unicast as IPAddressWire | undefined),
        portUnicast: sync?.port_unicast ?? 0,
        syncMode,
        tcpSynAck: formatDurationNsAsSeconds(sync?.tcp_syn_ack ?? DEFAULT_NS.tcpSynAck),
        tcpSyn: formatDurationNsAsSeconds(sync?.tcp_syn ?? DEFAULT_NS.tcpSyn),
        tcpFin: formatDurationNsAsSeconds(sync?.tcp_fin ?? DEFAULT_NS.tcpFin),
        tcp: formatDurationNsAsSeconds(sync?.tcp ?? DEFAULT_NS.tcp),
        udp: formatDurationNsAsSeconds(sync?.udp ?? DEFAULT_NS.udp),
        defaultTimeout: formatDurationNsAsSeconds(sync?.default ?? DEFAULT_NS.defaultTimeout),
        linkedAcls: config?.linked_acls ?? [],
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

type StateSubTab = 'configuration' | 'links' | 'states' | 'statistics';

const STATE_SUB_TABS: Array<{ id: StateSubTab; label: string }> = [
    { id: 'configuration', label: 'Configuration' },
    { id: 'links', label: 'Links' },
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
    statesQuery: StatesQuery;
    updateStatesQuery: (q: StatesQuery) => void;
    canLoadStates: boolean;
    stats: { ipv4?: MapStats; ipv6?: MapStats } | null;
}

const StatesTabBody: React.FC<StatesTabBodyProps> = ({
    currentName,
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

    const [tableSlotNode, setTableSlotNode] = useState<HTMLDivElement | null>(null);
    const tableSlotRef = useMemo(() => ({ current: tableSlotNode } as React.RefObject<HTMLElement | null>), [tableSlotNode]);
    const tableSlotHeight = useContainerHeight(tableSlotRef, 300, STATES_TABLE_BOTTOM_OFFSET);
    const tableScrollHeight = tableSlotHeight > 0 ? tableSlotHeight : undefined;
    const bodyHeight = tableScrollHeight !== undefined ? tableScrollHeight - STATES_TABLE_HEADER_HEIGHT : undefined;

    const queryKey = useMemo(() => JSON.stringify({
        currentName,
        isIpv6: statesQuery.isIpv6,
        layerIndex: statesQuery.layerIndex,
        direction: statesQuery.direction,
        includeExpired: statesQuery.includeExpired,
    }), [currentName, statesQuery]);

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
        if (!canLoadStates || !currentName) return;
        if (stateLoadingRef.current) return;
        if (!reset && !stateHasMoreRef.current) return;

        abortRef.current?.abort();
        const abort = new AbortController();
        abortRef.current = abort;
        const requestId = ++requestIdRef.current;

        setStateLoading(true);
        stateLoadingRef.current = true;

        const request: ListEntriesRequest = {
            config_name: currentName,
            is_ipv6: statesQuery.isIpv6,
            layer_index: statesQuery.layerIndex,
            include_expired: statesQuery.includeExpired,
            direction: statesQuery.direction,
            batch_size: STATES_TABLE_MAX_BATCH_SIZE,
            index: reset
                ? (statesQuery.direction === Direction.BACKWARD ? BACKWARD_RESET_CURSOR : 0)
                : stateCursorRef.current,
        };

        let shouldMarkLoaded = true;
        await new Promise<void>((resolve) => {
            API.fwstate.listEntriesPage(request, {
                onMessage: (res) => {
                    if (requestIdRef.current !== requestId) { resolve(); return; }
                    const generation = normalizeUnsignedInt(res.generation) ?? '0';
                    if (stateGenerationRef.current !== null && generation !== stateGenerationRef.current) {
                        shouldMarkLoaded = false;
                        resetView(true);
                        lastLoadedKeyRef.current = null;
                        inFlightKeyRef.current = null;
                        toaster.warning('fwstate-generation', 'State generation changed. Reload from start.');
                        resolve();
                        return;
                    }
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
                    resolve();
                },
                onError: (err) => {
                    if (abort.signal.aborted || requestIdRef.current !== requestId) { resolve(); return; }
                    toaster.error('fwstate-entries', 'Failed to load FWState entries', err);
                    resolve();
                },
                onEnd: () => resolve(),
            }, abort.signal);
        });

        if (requestIdRef.current === requestId) {
            abortRef.current = null;
            if (shouldMarkLoaded) {
                lastLoadedKeyRef.current = queryKey;
            }
            inFlightKeyRef.current = null;
            setStateLoading(false);
            stateLoadingRef.current = false;
        }
    }, [canLoadStates, currentName, queryKey, resetView, statesQuery]);

    useEffect(() => {
        resetView(true);
    }, [resetView, queryKey]);

    useEffect(() => {
        return () => { abortRef.current?.abort(); };
    }, []);

    useEffect(() => {
        if (!canLoadStates || !currentName || stateLoadingRef.current) return;
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

            <div
                ref={setTableSlotNode}
                className="fws-tablezone"
                style={tableScrollHeight !== undefined ? { height: tableScrollHeight } : undefined}
            >
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
                        style={bodyHeight !== undefined ? { height: bodyHeight } : undefined}
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
                                    : <div>This FWState config has no linked ACLs — states are not available.</div>
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

const FWStatePage: React.FC = () => {
    const navigate = useNavigate();
    const [searchParams, setSearchParams] = useSearchParams();
    const [loading, setLoading] = useState(true);
    const [activeSubTab, setActiveSubTab] = useState<StateSubTab>(() => getStateSubTab(searchParams));
    const [configs, setConfigs] = useState<Record<string, DraftConfig>>({});
    const [dirtyConfigs, setDirtyConfigs] = useState<Set<string>>(new Set());
    const [aclMeta, setAclMeta] = useState<AclMeta[]>([]);
    const [stats, setStats] = useState<{ ipv4?: MapStats; ipv6?: MapStats } | null>(null);
    const [addConfigOpen, setAddConfigOpen] = useState(false);
    const [deleteConfigOpen, setDeleteConfigOpen] = useState(false);
    const [pendingAclLink, setPendingAclLink] = useState<{
        aclName: string;
        linkedFwstateName: string | null;
    } | null>(null);

    const configsRef = useRef(configs);
    const dirtyConfigsRef = useRef(dirtyConfigs);
    const statsRequestIdRef = useRef(0);

    const configNames = useMemo(() => Object.keys(configs).sort((a, b) => a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' })), [configs]);
    const queryConfig = useMemo(() => searchParams.get(QP_CONFIG), [searchParams]);
    const currentName = useMemo(() => {
        if (queryConfig && (loading || configNames.includes(queryConfig))) {
            return queryConfig;
        }
        return configNames[0] || '';
    }, [configNames, queryConfig, loading]);
    const current = configs[currentName];
    const canLoadStates = Boolean(currentName && current && !current.isLocalOnly);
    const currentIsDirty = dirtyConfigs.has(currentName);
    const currentHasLinkedAcls = (current?.linkedAcls.length ?? 0) > 0;
    const anyDirty = dirtyConfigs.size > 0;

    const updateParams = useCallback((updates: Record<string, string | null>): void => {
        setSearchParams((prev) => {
            const next = new URLSearchParams(prev);
            for (const [key, value] of Object.entries(updates)) {
                if (value === null || value === '') {
                    next.delete(key);
                } else {
                    next.set(key, value);
                }
            }
            return next;
        }, { replace: true });
    }, [setSearchParams]);

    const updateActiveConfig = useCallback((name: string): void => {
        updateParams({ [QP_CONFIG]: name || null });
    }, [updateParams]);

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
            const fwConfigsResp = await API.fwstate.listConfigs();
            const fwNames = fwConfigsResp.configs ?? [];
            const fwFull = await Promise.all(fwNames.map(async (name) => ({ name, config: await API.fwstate.showConfig({ name }) })));
            const nextConfigs: Record<string, DraftConfig> = {};
            fwFull.forEach(({ name, config }) => {
                nextConfigs[name] = toDraftConfig(config, false);
            });

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

    const loadAclMeta = useCallback(async (): Promise<void> => {
        try {
            const aclListResp = await API.acl.listConfigs();
            const aclNames = aclListResp.configs ?? [];
            const baseRows = aclNames.map((name) => ({
                name,
                fwstateName: '',
                ruleCount: null,
                isLoaded: false,
                loadFailed: false,
            }));
            setAclMeta(baseRows);

            const nextAclMeta = await Promise.all(
                aclNames.map(async (name) => {
                    try {
                        const config = await API.acl.showConfig({ name });
                        const rules = config.rules ?? [];
                        return {
                            name,
                            fwstateName: config.fwstate_name ?? '',
                            ruleCount: rules.length,
                            isLoaded: true,
                            loadFailed: false,
                        };
                    } catch {
                        return {
                            name,
                            fwstateName: '',
                            ruleCount: null,
                            isLoaded: true,
                            loadFailed: true,
                        };
                    }
                })
            );
            setAclMeta(nextAclMeta);
        } catch (err) {
            toaster.error('fwstate-acl-load', 'Failed to load ACL metadata', err);
            setAclMeta([]);
        }
    }, []);

    useEffect(() => {
        let mounted = true;
        (async () => {
            await loadAll();
            if (!mounted) return;
            await loadAclMeta();
        })();
        return () => { mounted = false; };
    }, [loadAll, loadAclMeta]);

    useEffect(() => {
        const requestId = ++statsRequestIdRef.current;
        setStats(null);
        if (!currentName) return;
        API.fwstate.getStats({ name: currentName })
            .then((res) => {
                if (statsRequestIdRef.current !== requestId) return;
                setStats({ ipv4: res.ipv4_stats, ipv6: res.ipv6_stats });
            })
            .catch((err) => {
                if (statsRequestIdRef.current !== requestId) return;
                toaster.error('fwstate-stats', 'Failed to load FWState stats', err);
            });
    }, [currentName]);

    const hasOtherDirtyConfigs = useCallback((name: string): boolean => {
        return Array.from(dirtyConfigs).some((dirtyName) => dirtyName !== name);
    }, [dirtyConfigs]);

    const openLinkAclDialog = useCallback((aclName: string): void => {
        const aclMetaItem = aclMeta.find((item) => item.name === aclName);
        setPendingAclLink({
            aclName,
            linkedFwstateName: aclMetaItem?.fwstateName ? aclMetaItem.fwstateName : null,
        });
    }, [aclMeta]);

    const handleLinkAcl = useCallback(async (aclName: string): Promise<void> => {
        if (!currentName) return;
        if (dirtyConfigs.has(currentName)) {
            toaster.error('fwstate-dirty-link-current', 'Save or discard this config before linking ACLs.');
            return;
        }
        if (hasOtherDirtyConfigs(currentName)) {
            toaster.error('fwstate-dirty-link', 'Link blocked: there are unsaved changes in other configs.');
            return;
        }
        const aclNames = new Set(current?.linkedAcls ?? []);
        aclNames.add(aclName);
        try {
            await API.fwstate.linkFWState({ fwstate_name: currentName, acl_config_names: Array.from(aclNames) });
            await Promise.all([loadAll({ preserveDirty: true }), loadAclMeta()]);
        } catch (err) {
            toaster.error('fwstate-link-error', 'Failed to link ACL config', err);
        }
    }, [current?.linkedAcls, currentName, dirtyConfigs, hasOtherDirtyConfigs, loadAclMeta, loadAll]);

    const confirmLinkAcl = useCallback(async (): Promise<void> => {
        if (!pendingAclLink) return;
        const aclName = pendingAclLink.aclName;
        setPendingAclLink(null);
        await handleLinkAcl(aclName);
    }, [handleLinkAcl, pendingAclLink]);

    const counts = useMemo(() => {
        const m = new Map<string, number>();
        configNames.forEach((name) => { m.set(name, configs[name]?.linkedAcls.length ?? 0); });
        return m;
    }, [configNames, configs]);

    const updateCurrent = (patch: Partial<DraftConfig>): void => {
        if (!currentName) return;
        setConfigs((prev) => ({ ...prev, [currentName]: { ...prev[currentName], ...patch } }));
        setDirtyConfigs((prev) => new Set(prev).add(currentName));
    };

    const validateCurrent = (): boolean => {
        if (!current) return false;
        const durationFields = [current.tcpSynAck, current.tcpSyn, current.tcpFin, current.tcp, current.udp, current.defaultTimeout];
        const useMulticast = current.syncMode === 'multicast' || current.syncMode === 'both';
        const useUnicast = current.syncMode === 'unicast' || current.syncMode === 'both';
        if (current.mapIndexSize < 0 || current.mapExtraBucketCount < 0) return false;
        if (useMulticast && (current.portMulticast < 0 || current.portMulticast > 65535)) return false;
        if (useUnicast && (current.portUnicast < 0 || current.portUnicast > 65535)) return false;
        if (!isValidNonzeroIPv6Address(current.srcAddr)) return false;
        if (useMulticast && (!isValidNonzeroIPv6Address(current.dstAddrMulticast) || current.portMulticast === 0)) return false;
        if (useUnicast && (!isValidNonzeroIPv6Address(current.dstAddrUnicast) || current.portUnicast === 0)) return false;
        if (!isValidNonzeroMAC(current.dstEther)) return false;
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
        const useMulticast = current.syncMode === 'multicast' || current.syncMode === 'both';
        const useUnicast = current.syncMode === 'unicast' || current.syncMode === 'both';
        const syncConfig = {
            src_addr: stringToIPAddress(current.srcAddr),
            dst_ether: parseMACToBytes(current.dstEther),
            dst_addr_multicast: useMulticast ? stringToIPAddress(current.dstAddrMulticast) : zeroIPv6AddressWire(),
            port_multicast: useMulticast ? current.portMulticast : 0,
            dst_addr_unicast: useUnicast ? stringToIPAddress(current.dstAddrUnicast) : zeroIPv6AddressWire(),
            port_unicast: useUnicast ? current.portUnicast : 0,
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
                map_config: {
                    index_size: current.mapIndexSize,
                    extra_bucket_count: current.mapExtraBucketCount,
                },
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
        if (!currentName || (current?.linkedAcls.length ?? 0) > 0) return;
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

    const aclRows = useMemo(() => aclMeta.map((row) => ({ ...row, isLinkedHere: row.fwstateName === currentName })), [aclMeta, currentName]);

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

    const aclLinkDialogTitle = pendingAclLink
        ? pendingAclLink.linkedFwstateName && pendingAclLink.linkedFwstateName !== currentName
            ? 'Move ACL config'
            : 'Link ACL config'
        : '';
    const aclLinkDialogMessage = pendingAclLink
        ? pendingAclLink.linkedFwstateName && pendingAclLink.linkedFwstateName !== currentName
            ? `Move ACL "${pendingAclLink.aclName}" from "${pendingAclLink.linkedFwstateName}" to "${currentName}".`
            : `Link ACL "${pendingAclLink.aclName}" to FWState "${currentName}".`
        : '';

    const subTabHeaderAction = activeSubTab === 'configuration' ? (
        <>
            <button
                type="button"
                className="fw-tbl-action-btn fw-tbl-action-btn--save"
                title="Save config"
                aria-label="Save config"
                disabled={!currentIsDirty}
                onClick={handleSave}
            >
                <SaveIcon />
            </button>
            <button
                type="button"
                className="fw-tbl-action-btn fw-tbl-action-btn--delete"
                title="Delete config"
                aria-label="Delete config"
                disabled={!current || currentHasLinkedAcls}
                onClick={() => setDeleteConfigOpen(true)}
            >
                <TrashIcon />
            </button>
        </>
    ) : activeSubTab === 'links' ? (
        <Button view="flat" size="s" onClick={() => navigate('/modules/acl')}>Open ACL module</Button>
    ) : null;

    const pageHeader = (
        <Flex alignItems="center" gap={3} style={{ width: '100%' }}>
            <Text variant="header-1">FWState</Text>
            <Flex grow />
            <Button view="action" onClick={() => setAddConfigOpen(true)}>
                <Icon data={Plus} size={16} />
                Add Config
            </Button>
        </Flex>
    );

    if (loading) {
        return <PageLayout header={pageHeader}><PageLoader loading size="l" /></PageLayout>;
    }

    const configurationTab = current && (() => {
        const useMulticast = current.syncMode === 'multicast' || current.syncMode === 'both';
        const useUnicast = current.syncMode === 'unicast' || current.syncMode === 'both';
        const multicastAddrError = !useMulticast ? undefined : !isValidNonzeroIPv6Address(current.dstAddrMulticast) ? 'Non-zero IPv6 required' : undefined;
        const multicastPortError = !useMulticast ? undefined : current.portMulticast === 0 ? 'Port required' : current.portMulticast < 0 || current.portMulticast > 65535 ? '0..65535' : undefined;
        const unicastAddrError = !useUnicast ? undefined : !isValidNonzeroIPv6Address(current.dstAddrUnicast) ? 'Non-zero IPv6 required' : undefined;
        const unicastPortError = !useUnicast ? undefined : current.portUnicast === 0 ? 'Port required' : current.portUnicast < 0 || current.portUnicast > 65535 ? '0..65535' : undefined;

        return (
            <div className="fwstate-config-panel">
                <div className="fwstate-settings-top-row">
                    <div className="fwstate-config-section">
                        <div className="fwstate-config-section__head">
                            <Text variant="subheader-2">Map sizing</Text>
                        </div>
                        <div className="fwstate-field-grid fwstate-field-grid--map">
                            <label className="fwstate-field">
                                <Text variant="caption-2" color="secondary">Hash index slots</Text>
                                <TextInput type="number" value={String(current.mapIndexSize)} onUpdate={(v) => updateCurrent({ mapIndexSize: Number(v) })} />
                            </label>
                            <label className="fwstate-field">
                                <Text variant="caption-2" color="secondary">Overflow buckets</Text>
                                <TextInput type="number" value={String(current.mapExtraBucketCount)} onUpdate={(v) => updateCurrent({ mapExtraBucketCount: Number(v) })} />
                            </label>
                        </div>
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
                            <label className="fwstate-field fwstate-sync-grid__mac">
                                <Text variant="caption-2" color="secondary">Destination MAC</Text>
                                <TextInput value={current.dstEther} onUpdate={(dstEther) => updateCurrent({ dstEther })} error={!isValidNonzeroMAC(current.dstEther) ? 'Non-zero MAC required' : undefined} placeholder="aa:bb:cc:dd:ee:ff" />
                            </label>
                            <label className="fwstate-field fwstate-sync-grid__mode">
                                <Text variant="caption-2" color="secondary">Endpoint mode</Text>
                                <Select
                                    value={[current.syncMode]}
                                    options={[
                                        { value: 'multicast', content: 'Multicast' },
                                        { value: 'unicast', content: 'Unicast' },
                                        { value: 'both', content: 'Both' },
                                    ]}
                                    onUpdate={(value) => updateCurrent({ syncMode: (value[0] as DraftConfig['syncMode']) || 'multicast' })}
                                />
                            </label>
                            {useMulticast && (
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
                            )}
                            {useUnicast && (
                                <div className="fwstate-sync-grid__endpoint">
                                    <div className="fwstate-field">
                                        <Text variant="caption-2" color="secondary">Unicast endpoint</Text>
                                        <div className="fwstate-endpoint-row">
                                            <label className="fwstate-field">
                                                <Text variant="caption-2" color="secondary">Address</Text>
                                                <TextInput value={current.dstAddrUnicast} onUpdate={(dstAddrUnicast) => updateCurrent({ dstAddrUnicast })} error={unicastAddrError} placeholder="2001:db8::2" />
                                            </label>
                                            <label className="fwstate-field">
                                                <Text variant="caption-2" color="secondary">Port</Text>
                                                <TextInput type="number" value={String(current.portUnicast)} onUpdate={(v) => updateCurrent({ portUnicast: Number(v) })} error={unicastPortError} placeholder="2000" />
                                            </label>
                                        </div>
                                    </div>
                                </div>
                            )}
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

    const linksTab = current && (
        <section className="fwstate-acl-panel">
            <div className="fwstate-table-shell fwstate-acl-table-shell">
                <Table
                    data={aclRows}
                    columns={[
                        { id: 'name', name: 'ACL config', template: (row) => <span className="fwstate-table-cell">{row.name}</span> },
                        { id: 'fwstate', name: 'Current FWState', template: (row) => row.fwstateName ? <Label theme={row.isLinkedHere ? 'success' : 'warning'} size="s">{row.fwstateName}</Label> : <Label theme="unknown" size="s">{row.isLoaded ? 'unlinked' : 'Loading…'}</Label> },
                        { id: 'rules', name: 'Rules', template: (row) => <span className="fwstate-mono">{row.ruleCount === null ? (row.isLoaded ? (row.loadFailed ? '—' : 'Loading…') : 'Loading…') : row.ruleCount}</span> },
                        {
                            id: 'action',
                            name: 'Action',
                            template: (row) => {
                                if (row.isLinkedHere) return <Label theme="success" size="s">Linked</Label>;
                                if (!row.isLoaded) return <Button size="s" view="outlined" disabled>Loading…</Button>;
                                if (row.loadFailed) return <Text color="secondary" className="fwstate-table-cell">Unavailable</Text>;
                                return (
                                    <Button
                                        size="s"
                                        view="outlined"
                                        className="fwstate-acl-link-btn"
                                        onClick={() => openLinkAclDialog(row.name)}
                                    >
                                        {row.fwstateName ? 'Move here' : 'Link'}
                                    </Button>
                                );
                            },
                        },
                    ]}
                />
            </div>
            <p className="fws-link-note">
                Linking an ACL routes its <code>+state</code> / <code>?state</code> rule actions into this FWState map.
                One FWState may back multiple ACL configs.
            </p>
        </section>
    );

    const statisticsTab = current && (
        <section className="fws-stats-section">
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
        <PageLayout header={pageHeader}>
            <div className="fw-page">
                {configNames.length === 0 ? (
                    <div className="fw-empty-page">
                        <div className="fw-empty-page__message">No FWState configurations found.</div>
                        <Button view="action" onClick={() => setAddConfigOpen(true)}>Add Config</Button>
                    </div>
                ) : (
                    <>
                        <div className="fwstate-config-bar">
                            <div className="fwstate-config-bar__tabs">
                                <ConfigTabStrip
                                    configs={configNames}
                                    activeConfig={currentName}
                                    counts={counts}
                                    dirtyConfigs={dirtyConfigs}
                                    onSelect={updateActiveConfig}
                                    onAddConfig={() => setAddConfigOpen(true)}
                                />
                            </div>
                        </div>

                        <div className="fw-content fwstate-content">
                            {current && (
                                <div className="fwstate-settings-layout">
                                    <div
                                        className={`fwstate-panel fwstate-subtab-panel ${activeSubTab === 'states' ? 'fwstate-subtab-panel--states' : 'fwstate-subtab-panel--scroll'}`}
                                        role="tabpanel"
                                        id="fwstate-subtab-panel"
                                    >
                                        <div className="fwstate-subtab-frame">
                                            <div className="fwstate-subtab-frame__head">
                                                <div className="fw-tabs fwstate-sub-tabs" role="tablist" aria-label="FWState sub tabs">
                                                    {STATE_SUB_TABS.map((tab) => {
                                                        const isActive = tab.id === activeSubTab;
                                                        return (
                                                            <button
                                                                key={tab.id}
                                                                type="button"
                                                                role="tab"
                                                                aria-selected={isActive}
                                                                aria-controls="fwstate-subtab-panel"
                                                                className={`fw-tab${isActive ? ' fw-tab--active' : ''}`}
                                                                onClick={() => updateActiveSubTab(tab.id)}
                                                            >
                                                                <span className="fw-tab__label">{tab.label}</span>
                                                            </button>
                                                        );
                                                    })}
                                                </div>
                                                {subTabHeaderAction && <div className="fwstate-subtab-frame__actions">{subTabHeaderAction}</div>}
                                            </div>
                                        </div>
                                        {activeSubTab === 'configuration' && configurationTab}
                                        {activeSubTab === 'links' && linksTab}
                                        {activeSubTab === 'states' && (
                                            <StatesTabBody
                                                key={currentName}
                                                currentName={currentName}
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

            <ConfirmDialog
                open={pendingAclLink !== null}
                onClose={() => setPendingAclLink(null)}
                onConfirm={confirmLinkAcl}
                title={aclLinkDialogTitle}
                message={aclLinkDialogMessage}
                secondaryMessage={pendingAclLink?.linkedFwstateName && pendingAclLink.linkedFwstateName !== currentName
                    ? `This will detach ACL "${pendingAclLink.aclName}" from FWState "${pendingAclLink.linkedFwstateName}".`
                    : undefined}
                confirmText={pendingAclLink?.linkedFwstateName && pendingAclLink.linkedFwstateName !== currentName ? 'Move here' : 'Link'}
                cancelText="Cancel"
            />
        </PageLayout>
    );
};

export default FWStatePage;
