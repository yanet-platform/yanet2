import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
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
import { useVirtualizer } from '@tanstack/react-virtual';
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
import { IpAddressChip, ProtocolNumberChip } from '../_shared/chips';
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

const STATES_TABLE_BOTTOM_OFFSET = 68;
const STATES_TABLE_ROW_HEIGHT = 44;
const STATES_TABLE_HEADER_HEIGHT = 40;
const STATES_TABLE_OVERSCAN = 15;
const STATES_COL_IDX = 52;
const STATES_COL_SOURCE = 280;
const STATES_COL_DESTINATION = 280;
const STATES_COL_PROTO = 70;
const STATES_COL_SRC_FLAGS = 100;
const STATES_COL_DST_FLAGS = 100;
const STATES_COL_ORIGIN = 86;
const STATES_COL_PACKETS_FORWARD = 140;
const STATES_COL_PACKETS_BACKWARD = 186;
const STATES_COL_UPDATED = 168;
const STATES_COL_EXPIRED = 96;
const FWSTATE_STATES_TOTAL_WIDTH = STATES_COL_IDX +
    STATES_COL_SOURCE +
    STATES_COL_DESTINATION +
    STATES_COL_PROTO +
    STATES_COL_SRC_FLAGS +
    STATES_COL_DST_FLAGS +
    STATES_COL_ORIGIN +
    STATES_COL_PACKETS_FORWARD +
    STATES_COL_PACKETS_BACKWARD +
    STATES_COL_UPDATED +
    STATES_COL_EXPIRED;

const STATES_TABLE_MAX_BATCH_SIZE = 1000;

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

const formatUnsignedCount = (value: number | string | null | undefined, fallbackOnMissing = '0'): string => {
    if (value === undefined || value === null) {
        return fallbackOnMissing;
    }
    return normalizeUnsignedInt(value) ?? '-';
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
        [QP_FAMILY]: query.isIpv6 ? null : 'ipv4',
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

const renderIpChip = (ip: IPAddressWire | undefined): React.ReactElement => {
    const value = ipAddressToString(ip).trim();
    if (!value) {
        return <span className="fwstate-table-cell fwstate-table-cell--empty">-</span>;
    }
    return <IpAddressChip value={value} />;
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

const renderFlagChips = (flags: string[]): React.ReactElement => {
    if (flags.length === 0) {
        return <span className="fwstate-flag-chip fwstate-flag-chip--none">-</span>;
    }
    return (
        <span className="fwstate-flag-chip-list">
            {flags.map((flag) => <span key={flag} className="fwstate-flag-chip">{flag}</span>)}
        </span>
    );
};

interface FWStateStateColumn {
    id: string;
    label: string;
    width: number;
    align?: 'left' | 'center' | 'right';
    render: (item: FwStateEntry) => React.ReactElement;
}

const FWSTATE_STATES_COLUMNS: Array<FWStateStateColumn> = [
    { id: 'idx', label: 'IDX', width: STATES_COL_IDX, align: 'center', render: (item) => <span className="fwstate-mono">{formatStateIdx(item.idx)}</span> },
    { id: 'source', label: 'SOURCE', width: STATES_COL_SOURCE, render: (item) => renderIpChip(item.key?.src_addr as IPAddressWire | undefined) },
    { id: 'destination', label: 'DESTINATION', width: STATES_COL_DESTINATION, render: (item) => renderIpChip(item.key?.dst_addr as IPAddressWire | undefined) },
    {
        id: 'proto',
        label: 'PROTO',
        width: STATES_COL_PROTO,
        align: 'center',
        render: (item) => <ProtocolNumberChip proto={item.key?.proto} />,
    },
    { id: 'src_flags', label: 'SRC FLAGS', width: STATES_COL_SRC_FLAGS, render: (item) => renderFlagChips(decodeFlags(item.value?.flags).source) },
    { id: 'dst_flags', label: 'DST FLAGS', width: STATES_COL_DST_FLAGS, render: (item) => renderFlagChips(decodeFlags(item.value?.flags).destination) },
    {
        id: 'origin',
        label: 'ORIGIN',
        width: STATES_COL_ORIGIN,
        render: (item) => <span className="fwstate-table-cell">{item.value?.external ? 'external' : 'local'}</span>,
    },
    { id: 'packets_forward', label: 'PACKETS FWD', width: STATES_COL_PACKETS_FORWARD, align: 'right', render: (item) => <span className="fwstate-mono">{formatUnsignedCount(item.value?.packets_forward)}</span> },
    { id: 'packets_backward', label: 'PACKETS BACKWARD', width: STATES_COL_PACKETS_BACKWARD, align: 'right', render: (item) => <span className="fwstate-mono">{formatUnsignedCount(item.value?.packets_backward)}</span> },
    { id: 'updated', label: 'UPDATED', width: STATES_COL_UPDATED, render: (item) => <span className="fwstate-table-cell fwstate-updated">{formatNsUtc(item.value?.updated_at)}</span> },
    {
        id: 'expired',
        label: 'EXPIRED',
        width: STATES_COL_EXPIRED,
        align: 'center',
        render: (item) => (
            <span className={`fwstate-expired-pill ${item.expired ? 'fwstate-expired-pill--expired' : 'fwstate-expired-pill--active'}`}>
                {item.expired ? 'Expired' : 'Active'}
            </span>
        ),
    },
];

interface FWStateEntriesTableProps {
    rows: FwStateEntry[];
    loading: boolean;
    hasMore: boolean;
    height: number;
    onSetScrollRef: (node: HTMLDivElement | null) => void;
    onEndReached: () => void;
}

const FWStateEntriesTable: React.FC<FWStateEntriesTableProps> = ({ rows, loading, hasMore, height, onSetScrollRef, onEndReached }) => {
    const scrollRef = useRef<HTMLDivElement | null>(null);
    const headerInnerRef = useRef<HTMLDivElement | null>(null);
    const setScrollRef = useCallback((node: HTMLDivElement | null): void => {
        scrollRef.current = node;
        onSetScrollRef(node);
    }, [onSetScrollRef]);

    const rowVirtualizer = useVirtualizer({
        count: rows.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => STATES_TABLE_ROW_HEIGHT,
        overscan: STATES_TABLE_OVERSCAN,
    });

    const virtualRows = rowVirtualizer.getVirtualItems();

    const bodyHeight = Math.max(0, height - STATES_TABLE_HEADER_HEIGHT);

    const handleScroll = useCallback((event: React.UIEvent<HTMLDivElement>): void => {
        const { scrollTop, clientHeight, scrollHeight } = event.currentTarget;
        const { scrollLeft } = event.currentTarget;
        const headerInner = headerInnerRef.current;
        if (headerInner) {
            headerInner.style.transform = `translateX(-${scrollLeft}px)`;
        }
        if (scrollTop + clientHeight >= scrollHeight - 1 && hasMore && !loading) {
            onEndReached();
        }
    }, [hasMore, loading, onEndReached]);

    return (
        <div className="fw-tbl-wrap fwstate-states-vtbl">
            <div className="fw-tbl-header-row">
                <div className="fw-vtbl-header" style={{ height: STATES_TABLE_HEADER_HEIGHT }}>
                    <div
                        ref={headerInnerRef}
                        style={{
                            display: 'flex',
                            minWidth: FWSTATE_STATES_TOTAL_WIDTH,
                            height: '100%',
                            alignItems: 'center',
                            willChange: 'transform',
                        }}
                    >
                        {FWSTATE_STATES_COLUMNS.map((col) => (
                            <div
                                key={col.id}
                                style={{
                                    width: col.width,
                                    minWidth: col.width,
                                    flexShrink: 0,
                                    display: 'flex',
                                    alignItems: 'center',
                                    justifyContent: col.align === 'center' ? 'center' : col.align === 'right' ? 'flex-end' : 'flex-start',
                                    textAlign: col.align === 'center' ? 'center' : col.align === 'right' ? 'right' : 'left',
                                    paddingRight: col.align === 'center' || col.align === 'right' ? 8 : 0,
                                    paddingLeft: col.align === 'center' ? 0 : 0,
                                }}
                            >
                                <span className="fw-th-text">{col.label}</span>
                            </div>
                        ))}
                    </div>
                </div>
            </div>
            <div
                ref={setScrollRef}
                className="fw-vtbl-body"
                onScroll={handleScroll}
                style={bodyHeight > 0 ? { height: bodyHeight, flex: '0 0 auto' } : undefined}
            >
                {rows.length === 0 ? (
                    <div className="fw-table-empty">{loading ? 'Loading…' : 'No data'}</div>
                ) : (
                    <div style={{ height: rowVirtualizer.getTotalSize(), minWidth: FWSTATE_STATES_TOTAL_WIDTH, position: 'relative' }}>
                        {virtualRows.map((virtualRow) => {
                            const row = rows[virtualRow.index];
                            if (!row) return null;
                            return (
                                <div
                                    key={`${row.idx ?? 'row'}-${virtualRow.index}`}
                                    className="fw-vrow"
                                    style={{
                                        position: 'absolute',
                                        top: virtualRow.start,
                                        left: 0,
                                        height: STATES_TABLE_ROW_HEIGHT,
                                        minWidth: FWSTATE_STATES_TOTAL_WIDTH,
                                        width: '100%',
                                        display: 'flex',
                                        alignItems: 'center',
                                        borderBottom: '1px solid var(--fw-line)',
                                        paddingLeft: 4,
                                    }}
                                >
                                    {FWSTATE_STATES_COLUMNS.map((col) => (
                                        <div
                                            key={`${row.idx ?? 'row'}-${virtualRow.index}-${col.id}`}
                                            style={{
                                                width: col.width,
                                                minWidth: col.width,
                                                flexShrink: 0,
                                                display: 'flex',
                                                alignItems: 'center',
                                                justifyContent: col.align === 'center' ? 'center' : col.align === 'right' ? 'flex-end' : 'flex-start',
                                                textAlign: col.align === 'center' ? 'center' : col.align === 'right' ? 'right' : 'left',
                                                overflow: 'hidden',
                                                textOverflow: 'ellipsis',
                                                whiteSpace: 'nowrap',
                                                paddingRight: col.align === 'center' || col.align === 'right' ? 8 : 0,
                                                paddingLeft: 4,
                                                boxSizing: 'border-box',
                                            }}
                                        >
                                            <span style={{ display: 'inline-block', maxWidth: '100%' }}>{col.render(row)}</span>
                                        </div>
                                    ))}
                                </div>
                            );
                        })}
                    </div>
                )}
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
    const [stateRows, setStateRows] = useState<FwStateEntry[]>([]);
    const [stateGeneration, setStateGeneration] = useState<number | string | null>(null);
    const [stateCursor, setStateCursor] = useState<number>(0);
    const [stateHasMore, setStateHasMore] = useState(true);
    const [stateLoading, setStateLoading] = useState(false);
    const [pendingAclLink, setPendingAclLink] = useState<{
        aclName: string;
        linkedFwstateName: string | null;
    } | null>(null);
    const statesScrollRef = useRef<HTMLDivElement | null>(null);
    const [statesTableSlotNode, setStatesTableSlotNode] = useState<HTMLDivElement | null>(null);
    const statesTableSlotRef = useMemo(() => ({ current: statesTableSlotNode } as React.RefObject<HTMLElement | null>), [statesTableSlotNode]);
    const statesTableSlotHeight = useContainerHeight(statesTableSlotRef, 300, STATES_TABLE_BOTTOM_OFFSET);
    const setStatesTableSlotRef = useCallback((node: HTMLDivElement | null) => {
        setStatesTableSlotNode(node);
    }, []);

    const stateCursorRef = useRef(0);
    const stateRowsRef = useRef<FwStateEntry[]>([]);
    const stateLoadingRef = useRef(false);
    const stateHasMoreRef = useRef(true);
    const canLoadStatesRef = useRef(false);

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
    const configsRef = useRef(configs);
    const dirtyConfigsRef = useRef(dirtyConfigs);
    const statsRequestIdRef = useRef(0);
    const statesRequestIdRef = useRef(0);
    const statesAbortRef = useRef<AbortController | null>(null);
    const lastLoadedQueryKeyRef = useRef<string | null>(null);
    const inFlightStatesQueryKeyRef = useRef<string | null>(null);
    const stateGenerationRef = useRef<number | string | null>(null);
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
        if (Object.keys(statesQueryParamUpdates).length > 0) {
            updateParams(statesQueryParamUpdates);
        }
    }, [statesQueryParamUpdates, updateParams]);

    useEffect(() => {
        const updates: Record<string, string | null> = {};
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
    }, [activeSubTab, configNames.length, currentName, loading, queryConfig, searchParams, updateParams]);
    useUnsavedChangesBlocker(anyDirty);

    useEffect(() => {
        configsRef.current = configs;
    }, [configs]);

    useEffect(() => {
        dirtyConfigsRef.current = dirtyConfigs;
    }, [dirtyConfigs]);

    useEffect(() => {
        stateGenerationRef.current = stateGeneration;
    }, [stateGeneration]);

    useEffect(() => {
        canLoadStatesRef.current = canLoadStates;
    }, [canLoadStates]);

    useEffect(() => {
        stateLoadingRef.current = stateLoading;
    }, [stateLoading]);

    useEffect(() => {
        stateHasMoreRef.current = stateHasMore;
    }, [stateHasMore]);

    useEffect(() => {
        stateRowsRef.current = stateRows;
    }, [stateRows]);

    useEffect(() => {
        stateCursorRef.current = stateCursor;
    }, [stateCursor]);

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
                setDirtyConfigs(
                    new Set(Array.from(preservedDirtyNames).filter((name) => Boolean(mergedConfigs[name])))
                );
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
        return () => {
            mounted = false;
        };
    }, [loadAll, loadAclMeta]);

    const resetStatesView = useCallback((options?: { clearLoading?: boolean }): void => {
        statesAbortRef.current?.abort();
        statesAbortRef.current = null;
        statesRequestIdRef.current += 1;
        inFlightStatesQueryKeyRef.current = null;
        stateGenerationRef.current = null;
        setStateRows([]);
        stateRowsRef.current = [];
        setStateCursor(0);
        stateCursorRef.current = 0;
        setStateHasMore(true);
        stateHasMoreRef.current = true;
        setStateGeneration(null);
        if (options?.clearLoading) {
            setStateLoading(false);
            stateLoadingRef.current = false;
        }
    }, []);

    const statesQueryKey = useMemo(() => {
        return JSON.stringify({
            currentName,
            isIpv6: statesQuery.isIpv6,
            layerIndex: statesQuery.layerIndex,
            direction: statesQuery.direction,
            includeExpired: statesQuery.includeExpired,
        });
    }, [currentName, statesQuery.direction, statesQuery.includeExpired, statesQuery.isIpv6, statesQuery.layerIndex]);

    useEffect(() => {
        resetStatesView({ clearLoading: true });
    }, [resetStatesView, statesQueryKey]);

    useEffect(() => {
        return () => {
            statesAbortRef.current?.abort();
        };
    }, []);

    useEffect(() => {
        const requestId = ++statsRequestIdRef.current;
        setStats(null);
        if (!currentName) return;
        API.fwstate.getStats({ name: currentName })
            .then((res) => {
                if (statsRequestIdRef.current !== requestId) {
                    return;
                }
                setStats({ ipv4: res.ipv4_stats, ipv6: res.ipv6_stats });
            })
            .catch((err) => {
                if (statsRequestIdRef.current !== requestId) {
                    return;
                }
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
            await Promise.all([
                loadAll({ preserveDirty: true }),
                loadAclMeta(),
            ]);
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

    useEffect(() => {
        const isEditableTarget = (target: EventTarget | null): boolean => {
            if (!(target instanceof HTMLElement)) return false;
            const tagName = target.tagName.toLowerCase();
            if (tagName === 'input' || tagName === 'textarea' || tagName === 'select') return true;
            return target.isContentEditable;
        };

        const onKeyDown = (event: KeyboardEvent) => {
            if (event.defaultPrevented) return;
            if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
            if (isEditableTarget(event.target)) return;

            const key = event.key.toLowerCase();
            if (key === '4') {
                event.preventDefault();
                if (statesQuery.isIpv6) {
                    updateStatesQuery({ ...statesQuery, isIpv6: false });
                }
                return;
            }
            if (key === '6') {
                event.preventDefault();
                if (!statesQuery.isIpv6) {
                    updateStatesQuery({ ...statesQuery, isIpv6: true });
                }
                return;
            }
            if (key === 'f') {
                event.preventDefault();
                if (statesQuery.direction !== Direction.FORWARD) {
                    updateStatesQuery({ ...statesQuery, direction: Direction.FORWARD });
                }
                return;
            }
            if (key === 'b') {
                event.preventDefault();
                if (statesQuery.direction !== Direction.BACKWARD) {
                    updateStatesQuery({ ...statesQuery, direction: Direction.BACKWARD });
                }
                return;
            }
            if (key === 'e') {
                event.preventDefault();
                updateStatesQuery({ ...statesQuery, includeExpired: !statesQuery.includeExpired });
            }
        };

        document.addEventListener('keydown', onKeyDown);
        return () => {
            document.removeEventListener('keydown', onKeyDown);
        };
    }, [statesQuery, updateStatesQuery]);

    const counts = useMemo(() => {
        const m = new Map<string, number>();
        configNames.forEach((name) => {
            m.set(name, configs[name]?.linkedAcls.length ?? 0);
        });
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
        const multicastAddrValid = isValidNonzeroIPv6Address(current.dstAddrMulticast);
        const unicastAddrValid = isValidNonzeroIPv6Address(current.dstAddrUnicast);
        if (current.mapIndexSize < 0 || current.mapExtraBucketCount < 0) return false;
        if (useMulticast && (current.portMulticast < 0 || current.portMulticast > 65535)) return false;
        if (useUnicast && (current.portUnicast < 0 || current.portUnicast > 65535)) return false;
        if (!isValidNonzeroIPv6Address(current.srcAddr)) return false;
        if (useMulticast && (!multicastAddrValid || current.portMulticast === 0)) return false;
        if (useUnicast && (!unicastAddrValid || current.portUnicast === 0)) return false;
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

    const requestStatesPrefetch = (): void => {
        const container = statesScrollRef.current;
        if (!container || !canLoadStatesRef.current || stateLoadingRef.current || !stateHasMoreRef.current || !currentName) {
            return;
        }
        const { scrollHeight, clientHeight, scrollTop } = container;
        if (scrollTop + clientHeight >= scrollHeight - 1) {
            void loadStatesPage(false);
        }
    };

    const loadStatesPage = useCallback(async (reset: boolean): Promise<void> => {
        if (!canLoadStates || !currentName) return;
        if (stateLoadingRef.current) return;
        if (!reset && !stateHasMoreRef.current) return;
        statesAbortRef.current?.abort();
        const abortController = new AbortController();
        statesAbortRef.current = abortController;
        const requestId = ++statesRequestIdRef.current;
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
                    if (statesRequestIdRef.current !== requestId) {
                        resolve();
                        return;
                    }
                    const generation = normalizeUnsignedInt(res.generation) ?? '0';
                    if (stateGenerationRef.current !== null && generation !== stateGenerationRef.current) {
                        shouldMarkLoaded = false;
                        resetStatesView({ clearLoading: true });
                        lastLoadedQueryKeyRef.current = null;
                        inFlightStatesQueryKeyRef.current = null;
                        toaster.warning('fwstate-generation', 'State generation changed. Reload from start.');
                        resolve();
                        return;
                    }
                    setStateGeneration(generation);
                    const rows = res.entries ?? [];
                    const nextRows = reset ? rows : [...stateRowsRef.current, ...rows];
                    const nextCursor = normalizeUnsignedIntToNumber(res.index);
                    const nextHasMore = Boolean(res.has_more);
                    setStateRows(nextRows);
                    stateRowsRef.current = nextRows;
                    setStateCursor(nextCursor);
                    stateCursorRef.current = nextCursor;
                    setStateHasMore(nextHasMore);
                    stateHasMoreRef.current = nextHasMore;
                    resolve();
                },
                onError: (err) => {
                    if (abortController.signal.aborted || statesRequestIdRef.current !== requestId) {
                        resolve();
                        return;
                    }
                    lastLoadedQueryKeyRef.current = statesQueryKey;
                    toaster.error('fwstate-entries', 'Failed to load FWState entries', err);
                    resolve();
                },
                onEnd: () => resolve(),
            }, abortController.signal);
        });
        if (statesRequestIdRef.current === requestId) {
            statesAbortRef.current = null;
            if (shouldMarkLoaded) {
                lastLoadedQueryKeyRef.current = statesQueryKey;
            }
            inFlightStatesQueryKeyRef.current = null;
            setStateLoading(false);
            stateLoadingRef.current = false;
            if (shouldMarkLoaded) {
                requestAnimationFrame(() => {
                    requestStatesPrefetch();
                });
            }
        }
    }, [
        canLoadStates,
        currentName,
        resetStatesView,
        statesQuery.direction,
        statesQuery.includeExpired,
        statesQuery.isIpv6,
        statesQuery.layerIndex,
        statesQueryKey,
    ]);

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

    useEffect(() => {
        if (!canLoadStates || !currentName || stateLoading) {
            return;
        }
        if (lastLoadedQueryKeyRef.current === statesQueryKey) {
            return;
        }
        if (inFlightStatesQueryKeyRef.current === statesQueryKey) {
            return;
        }
        inFlightStatesQueryKeyRef.current = statesQueryKey;
        void loadStatesPage(true);
    }, [canLoadStates, currentName, loadStatesPage, stateLoading, statesQueryKey]);

    useEffect(() => {
        if (canLoadStates) {
            return;
        }
        resetStatesView({ clearLoading: true });
    }, [canLoadStates, resetStatesView]);

    const statesTableHeight = statesTableSlotHeight > 0 ? statesTableSlotHeight : 0;
    const setStatesScrollRef = useCallback((node: HTMLDivElement | null): void => {
        statesScrollRef.current = node;
    }, []);

    const statesTab = current && (
        <section className="fwstate-states-table-panel">
            <div className="fwstate-states-toolbar">
                <div className="fwstate-states-toolbar__control fwstate-states-toolbar__control--field fwstate-states-toolbar__control--field-family">
                    <Text className="fwstate-states-toolbar__label">Address family</Text>
                    <SegmentedRadioGroup
                        size="m"
                        width="max"
                        className="fwstate-states-toolbar__family"
                        value={statesQuery.isIpv6 ? 'ipv6' : 'ipv4'}
                        onUpdate={(value) => updateStatesQuery({ ...statesQuery, isIpv6: value === 'ipv6' })}
                    >
                        <SegmentedRadioGroup.Option value="ipv6" content="IPv6" />
                        <SegmentedRadioGroup.Option value="ipv4" content="IPv4" />
                    </SegmentedRadioGroup>
                </div>
                <div className="fwstate-states-toolbar__control fwstate-states-toolbar__control--field fwstate-states-toolbar__control--field-direction">
                    <Text className="fwstate-states-toolbar__label">Direction</Text>
                    <div title="Direction (f/b)">
                    <Select
                        value={[String(statesQuery.direction)]}
                        onUpdate={(v) => updateStatesQuery({ ...statesQuery, direction: Number(v[0] ?? 0) as Direction })}
                        options={[{ value: String(Direction.FORWARD), content: 'forward' }, { value: String(Direction.BACKWARD), content: 'backward' }]}
                    />
                    </div>
                </div>
                <div className="fwstate-states-toolbar__control fwstate-states-toolbar__control--field fwstate-states-toolbar__control--field-layer">
                    <Text className="fwstate-states-toolbar__label">State layer</Text>
                    <div title="State layer">
                        <TextInput
                            type="number"
                            value={String(statesQuery.layerIndex)}
                            onUpdate={(v) => updateStatesQuery({ ...statesQuery, layerIndex: normalizeUnsignedIntToNumber(v) })}
                        />
                    </div>
                </div>
                <div className="fwstate-states-toolbar__control fwstate-states-toolbar__control--switch">
                    <Text className="fwstate-states-toolbar__label">Include expired</Text>
                    <div title="Include expired (e)">
                        <Switch checked={statesQuery.includeExpired} onUpdate={(includeExpired) => updateStatesQuery({ ...statesQuery, includeExpired })} />
                    </div>
                </div>
            </div>
            <div
                ref={setStatesTableSlotRef}
                className="fwstate-states-table-slot"
                style={statesTableSlotHeight > 0 ? { height: statesTableSlotHeight, maxHeight: statesTableSlotHeight } : undefined}
            >
                <FWStateEntriesTable
                    rows={stateRows}
                    loading={stateLoading}
                    hasMore={stateHasMore}
                    height={statesTableHeight}
                    onSetScrollRef={setStatesScrollRef}
                    onEndReached={requestStatesPrefetch}
                />
            </div>
            <div className="fwstate-states-footer">
                <Text className="fwstate-states-footer__text">{stateLoading ? 'Loading…' : `Shown ${stateRows.length} rows`}</Text>
                <Text className="fwstate-states-footer__text fwstate-states-footer__text--secondary">
                    {stateLoading || stateRows.length === 0 || stateHasMore ? '\u00a0' : 'End of entries.'}
                </Text>
            </div>
        </section>
    );

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
                                if (row.isLinkedHere) {
                                    return <Label theme="success" size="s">Linked</Label>;
                                }
                                if (!row.isLoaded) {
                                    return (
                                        <Button size="s" view="outlined" disabled>
                                            Loading…
                                        </Button>
                                    );
                                }
                                if (row.loadFailed) {
                                    return (
                                        <Text color="secondary" className="fwstate-table-cell">
                                            Unavailable
                                        </Text>
                                    );
                                }
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
        </section>
    );

    const statisticsTab = current && (
        <section className="fwstate-stats-compare-wrap">
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

    const configurationTab = current && (() => {
        const useMulticast = current.syncMode === 'multicast' || current.syncMode === 'both';
        const useUnicast = current.syncMode === 'unicast' || current.syncMode === 'both';
        const multicastAddrError = !useMulticast
            ? undefined
            : !isValidNonzeroIPv6Address(current.dstAddrMulticast)
                ? 'Non-zero IPv6 required'
                : undefined;
        const multicastPortError = !useMulticast
            ? undefined
            : current.portMulticast < 0 || current.portMulticast > 65535
                ? '0..65535'
                : current.portMulticast === 0
                    ? 'Port required'
                    : undefined;
        const unicastAddrError = !useUnicast
            ? undefined
            : !isValidNonzeroIPv6Address(current.dstAddrUnicast)
                ? 'Non-zero IPv6 required'
                : undefined;
        const unicastPortError = !useUnicast
            ? undefined
            : current.portUnicast < 0 || current.portUnicast > 65535
                ? '0..65535'
                : current.portUnicast === 0
                    ? 'Port required'
                    : undefined;

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
                            <TextInput
                                type="number"
                                value={current.tcpSynAck}
                                onUpdate={(tcpSynAck) => updateCurrent({ tcpSynAck })}
                                error={parseDurationToNs(current.tcpSynAck) ? undefined : 'Enter seconds'}
                                endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>}
                            />
                        </label>
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">TCP SYN</Text>
                            <TextInput
                                type="number"
                                value={current.tcpSyn}
                                onUpdate={(tcpSyn) => updateCurrent({ tcpSyn })}
                                error={parseDurationToNs(current.tcpSyn) ? undefined : 'Enter seconds'}
                                endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>}
                            />
                        </label>
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">TCP FIN</Text>
                            <TextInput
                                type="number"
                                value={current.tcpFin}
                                onUpdate={(tcpFin) => updateCurrent({ tcpFin })}
                                error={parseDurationToNs(current.tcpFin) ? undefined : 'Enter seconds'}
                                endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>}
                            />
                        </label>
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">TCP established</Text>
                            <TextInput
                                type="number"
                                value={current.tcp}
                                onUpdate={(tcp) => updateCurrent({ tcp })}
                                error={parseDurationToNs(current.tcp) ? undefined : 'Enter seconds'}
                                endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>}
                            />
                        </label>
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">UDP</Text>
                            <TextInput
                                type="number"
                                value={current.udp}
                                onUpdate={(udp) => updateCurrent({ udp })}
                                error={parseDurationToNs(current.udp) ? undefined : 'Enter seconds'}
                                endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>}
                            />
                        </label>
                        <label className="fwstate-field">
                            <Text variant="caption-2" color="secondary">Default</Text>
                            <TextInput
                                type="number"
                                value={current.defaultTimeout}
                                onUpdate={(defaultTimeout) => updateCurrent({ defaultTimeout })}
                                error={parseDurationToNs(current.defaultTimeout) ? undefined : 'Enter seconds'}
                                endContent={<Text className="fwstate-timeout-unit" variant="caption-2" color="secondary">s</Text>}
                            />
                        </label>
                    </div>
                </div>
            </div>
        );
    })();

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

    if (loading) {
        return <PageLayout header={pageHeader}><PageLoader loading size="l" /></PageLayout>;
    }

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
                                        {activeSubTab === 'states' && statesTab}
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
