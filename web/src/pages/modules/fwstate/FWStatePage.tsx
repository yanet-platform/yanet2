import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Flex, Icon, Label, Select, Switch, Table, Text, TextInput } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { useNavigate } from 'react-router-dom';
import { API } from '../../../api';
import { Direction, type FwStateEntry, type ListEntriesRequest, type MapStats } from '../../../api/fwstate';
import { ConfirmDialog, ConfigTabStrip, PageLayout, PageLoader } from '../../../components';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import { ipAddressToString, isValidIPAddress, parseIPToBytes, stringToIPAddress, type IPAddressWire } from '../../../utils/netip';
import { formatMACFromBytes, parseMACToBytes } from '../../../utils/mac';
import { toaster } from '../../../utils';
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

const FLAG_TONES: Array<{ bit: number; label: string }> = [
    { bit: 0x01, label: 'FIN' },
    { bit: 0x02, label: 'SYN' },
    { bit: 0x04, label: 'RST' },
    { bit: 0x08, label: 'ACK' },
];

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

const FWStatePage: React.FC = () => {
    const navigate = useNavigate();
    const [loading, setLoading] = useState(true);
    const [activeConfig, setActiveConfig] = useState('');
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
    const [statesQuery, setStatesQuery] = useState({
        isIpv6: true,
        layerIndex: 0,
        direction: Direction.FORWARD,
        includeExpired: false,
    });
    const [pendingAclLink, setPendingAclLink] = useState<{
        aclName: string;
        linkedFwstateName: string | null;
    } | null>(null);
    const statesScrollRef = useRef<HTMLDivElement | null>(null);

    const configNames = useMemo(() => Object.keys(configs).sort((a, b) => a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' })), [configs]);
    const currentName = activeConfig || configNames[0] || '';
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
                setActiveConfig((prev) => {
                    if (prev && !skipDirtyNames.has(prev) && mergedConfigs[prev]) {
                        return prev;
                    }
                    return Object.keys(mergedConfigs)[0] ?? '';
                });
            } else {
                setConfigs(nextConfigs);
                setDirtyConfigs(new Set());
                setActiveConfig((prev) => {
                    return prev && nextConfigs[prev] ? prev : Object.keys(nextConfigs)[0] ?? '';
                });
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
        setStateCursor(0);
        setStateHasMore(true);
        setStateGeneration(null);
        if (options?.clearLoading) {
            setStateLoading(false);
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
                setStatesQuery((prev) => (prev.isIpv6 ? { ...prev, isIpv6: false } : prev));
                return;
            }
            if (key === '6') {
                event.preventDefault();
                setStatesQuery((prev) => (prev.isIpv6 ? prev : { ...prev, isIpv6: true }));
                return;
            }
            if (key === 'f') {
                event.preventDefault();
                setStatesQuery((prev) => (prev.direction === Direction.FORWARD ? prev : { ...prev, direction: Direction.FORWARD }));
                return;
            }
            if (key === 'b') {
                event.preventDefault();
                setStatesQuery((prev) => (prev.direction === Direction.BACKWARD ? prev : { ...prev, direction: Direction.BACKWARD }));
                return;
            }
            if (key === 'e') {
                event.preventDefault();
                setStatesQuery((prev) => ({ ...prev, includeExpired: !prev.includeExpired }));
            }
        };

        document.addEventListener('keydown', onKeyDown);
        return () => {
            document.removeEventListener('keydown', onKeyDown);
        };
    }, [setStatesQuery]);

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
            setActiveConfig(currentName);
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
            setActiveConfig(remainingNames[0] ?? '');
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

    const getStatesBatchSize = (): number => {
        const containerHeight = statesScrollRef.current?.clientHeight ?? 0;
        const estimatedRowHeight = 42;
        const visibleRows = Math.max(10, Math.floor(containerHeight / estimatedRowHeight));
        return Math.max(30, Math.min(500, visibleRows * 3));
    };

    const loadStatesPage = useCallback(async (reset: boolean): Promise<void> => {
        if (!canLoadStates || !currentName) return;
        if (stateLoading) return;
        if (!reset && !stateHasMore) return;
        statesAbortRef.current?.abort();
        const abortController = new AbortController();
        statesAbortRef.current = abortController;
        const requestId = ++statesRequestIdRef.current;
        setStateLoading(true);
        const request: ListEntriesRequest = {
            config_name: currentName,
            is_ipv6: statesQuery.isIpv6,
            layer_index: statesQuery.layerIndex,
            include_expired: statesQuery.includeExpired,
            direction: statesQuery.direction,
            batch_size: getStatesBatchSize(),
            index: reset
                ? (statesQuery.direction === Direction.BACKWARD ? BACKWARD_RESET_CURSOR : 0)
                : stateCursor,
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
                    setStateRows((prev) => reset ? rows : [...prev, ...rows]);
                    setStateCursor(normalizeUnsignedIntToNumber(res.index));
                    setStateHasMore(Boolean(res.has_more));
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
        }
    }, [
        canLoadStates,
        currentName,
        resetStatesView,
        stateCursor,
        stateHasMore,
        stateLoading,
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
            row('Max deadline', (s) => s?.max_deadline ?? '-'),
            row('Memory used', (s) => s?.memory_used ?? '-'),
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

    const handleStatesScroll = useCallback(() => {
        const container = statesScrollRef.current;
        if (!container || stateLoading || !stateHasMore || !canLoadStates || !currentName || stateRows.length === 0) {
            return;
        }
        const threshold = 120;
        if (container.scrollTop + container.clientHeight >= container.scrollHeight - threshold) {
            void loadStatesPage(false);
        }
    }, [canLoadStates, currentName, stateHasMore, stateLoading, stateRows.length, stateCursor, stateGeneration, statesQuery.direction, statesQuery.includeExpired, statesQuery.isIpv6, statesQuery.layerIndex]);

    const stateColumns = [
        { id: 'idx', name: 'IDX', template: (item: FwStateEntry) => <span className="fwstate-mono">{formatStateIdx(item.idx)}</span> },
        { id: 'source', name: 'SOURCE', template: (item: FwStateEntry) => renderIpChip(item.key?.src_addr as IPAddressWire | undefined) },
        { id: 'destination', name: 'DESTINATION', template: (item: FwStateEntry) => renderIpChip(item.key?.dst_addr as IPAddressWire | undefined) },
        {
            id: 'proto',
            name: 'PROTO',
            template: (item: FwStateEntry) => <ProtocolNumberChip proto={item.key?.proto} />,
        },
        {
            id: 'src_flags',
            name: 'SRC FLAGS',
            template: (item: FwStateEntry) => renderFlagChips(decodeFlags(item.value?.flags).source),
        },
        {
            id: 'dst_flags',
            name: 'DST FLAGS',
            template: (item: FwStateEntry) => renderFlagChips(decodeFlags(item.value?.flags).destination),
        },
        { id: 'origin', name: 'ORIGIN', template: (item: FwStateEntry) => <span className="fwstate-table-cell">{item.value?.external ? 'external' : 'local'}</span> },
        { id: 'packets_forward', name: 'PACKETS FWD', template: (item: FwStateEntry) => <span className="fwstate-mono">{formatUnsignedCount(item.value?.packets_forward)}</span> },
        { id: 'packets_backward', name: 'PACKETS BACKWARD', template: (item: FwStateEntry) => <span className="fwstate-mono">{formatUnsignedCount(item.value?.packets_backward)}</span> },
        { id: 'updated', name: 'UPDATED', template: (item: FwStateEntry) => <span className="fwstate-mono fwstate-updated">{formatNsUtc(item.value?.updated_at)}</span> },
        {
            id: 'expired',
            name: 'EXPIRED',
            template: (item: FwStateEntry) => (
                <span className={`fwstate-expired-pill ${item.expired ? 'fwstate-expired-pill--expired' : 'fwstate-expired-pill--active'}`}>
                    {item.expired ? 'Expired' : 'Active'}
                </span>
            ),
        },
    ];

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
                                    onSelect={setActiveConfig}
                                    onAddConfig={() => setAddConfigOpen(true)}
                                />
                            </div>
                        </div>

                        <div className="fw-content fwstate-content">
                            {current && (
                                (() => {
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
                                        <div className="fwstate-settings-layout">
                                            <div className="fwstate-panel fwstate-states-table-panel">
                                                <div className="fwstate-panel-head">
                                                    <Text variant="subheader-2">States</Text>
                                                </div>
                                                <div className="fwstate-states-toolbar">
                                                    <div className="fwstate-states-toolbar__control fwstate-states-toolbar__control--field fwstate-states-toolbar__control--field-family">
                                                        <Text className="fwstate-states-toolbar__label">Address family</Text>
                                                            <div className="fwstate-states-toolbar__family">
                                                                <Button
                                                                    view="outlined"
                                                                    size="s"
                                                                    className={`fwstate-states-toolbar__family-btn${statesQuery.isIpv6 ? ' fwstate-states-toolbar__family-btn--active' : ''}`}
                                                                    onClick={() => setStatesQuery((prev) => ({ ...prev, isIpv6: true }))}
                                                                    title="IPv6 (6)"
                                                                >
                                                                    IPv6
                                                                </Button>
                                                                <Button
                                                                    view="outlined"
                                                                    size="s"
                                                                    className={`fwstate-states-toolbar__family-btn${statesQuery.isIpv6 ? '' : ' fwstate-states-toolbar__family-btn--active'}`}
                                                                    onClick={() => setStatesQuery((prev) => ({ ...prev, isIpv6: false }))}
                                                                    title="IPv4 (4)"
                                                                >
                                                                    IPv4
                                                                </Button>
                                                            </div>
                                                        </div>
                                                    <div className="fwstate-states-toolbar__control fwstate-states-toolbar__control--field fwstate-states-toolbar__control--field-direction">
                                                        <Text className="fwstate-states-toolbar__label">Direction</Text>
                                                        <div title="Direction (f/b)">
                                                            <Select
                                                                value={[String(statesQuery.direction)]}
                                                                onUpdate={(v) => setStatesQuery((prev) => ({ ...prev, direction: Number(v[0] ?? 0) as Direction }))}
                                                                options={[{ value: String(Direction.FORWARD), content: 'forward' }, { value: String(Direction.BACKWARD), content: 'backward' }]}
                                                            />
                                                        </div>
                                                    </div>
                                                    <div className="fwstate-states-toolbar__control fwstate-states-toolbar__control--switch">
                                                        <Text className="fwstate-states-toolbar__label">Include expired</Text>
                                                        <div title="Include expired (e)">
                                                            <Switch checked={statesQuery.includeExpired} onUpdate={(includeExpired) => setStatesQuery((prev) => ({ ...prev, includeExpired }))} />
                                                        </div>
                                                    </div>
                                                </div>
                                                <details className="fwstate-states-advanced-details">
                                                    <summary className="fwstate-states-advanced-details__summary">
                                                        <Text variant="caption-2" color="secondary">Advanced filters</Text>
                                                    </summary>
                                                    <div className="fwstate-states-advanced-details__content">
                                                        <div className="fwstate-states-toolbar__control fwstate-states-toolbar__control--field fwstate-states-toolbar__control--field-layer">
                                                            <Text className="fwstate-states-toolbar__label">State layer</Text>
                                                            <div title="State layer (0 = active layer)">
                                                                <TextInput
                                                                    type="number"
                                                                    value={String(statesQuery.layerIndex)}
                                                                    onUpdate={(v) => setStatesQuery((prev) => ({ ...prev, layerIndex: Math.max(0, Number(v) || 0) }))}
                                                                />
                                                            </div>
                                                            <Text className="fwstate-states-toolbar__hint">0 = active layer</Text>
                                                        </div>
                                                    </div>
                                                </details>
                                                <div className="fwstate-table-shell fwstate-table-shell--scroll" ref={statesScrollRef} onScroll={handleStatesScroll}>
                                                    <div className="fwstate-states-table-content">
                                                        <Table data={stateRows} columns={stateColumns} emptyMessage="" />
                                                    </div>
                                                    {!stateLoading && stateRows.length === 0 && <div className="fwstate-states-empty-overlay">No data</div>}
                                                </div>
                                                <div className="fwstate-states-footer">
                                                    <Text className="fwstate-states-footer__text">{stateLoading ? 'Loading…' : `Shown ${stateRows.length} rows`}</Text>
                                                    <Text className="fwstate-states-footer__text fwstate-states-footer__text--secondary">
                                                        {stateLoading || stateRows.length === 0 || stateHasMore ? '\u00a0' : 'End of entries.'}
                                                    </Text>
                                                </div>
                                            </div>

                                            <div className="fwstate-operational-secondary">
                                                <div className="fwstate-panel fwstate-acl-panel">
                                                    <div className="fwstate-panel-head fwstate-panel-head--split">
                                                        <Text variant="subheader-2">ACL links</Text>
                                                        <Button view="flat" size="s" onClick={() => navigate('/modules/acl')}>Open ACL module</Button>
                                                    </div>
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
                                                </div>

                                                <div className="fwstate-panel">
                                                    <div className="fwstate-panel-head">
                                                        <Text variant="subheader-2">State map stats</Text>
                                                    </div>
                                                    <div className="fwstate-stats-compare">
                                                        <div className="fwstate-stats-compare__head">Metric</div>
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
                                                    {statsNote && (
                                                        <Text className="fwstate-stats-note" color="secondary">
                                                            {statsNote}
                                                        </Text>
                                                    )}
                                                </div>
                                            </div>
                                            <details className="fwstate-config-details">
                                                <summary className="fwstate-config-details__summary">
                                                    <div className="fwstate-config-details__summary-inner">
                                                        <Text variant="subheader-2">Configuration</Text>
                                                        <div className="fwstate-config-details__summary-actions">
                                                            <button
                                                                type="button"
                                                                className="fw-tbl-action-btn fw-tbl-action-btn--save"
                                                                title="Save config"
                                                                aria-label="Save config"
                                                                disabled={!currentIsDirty}
                                                                onClick={(event) => {
                                                                    event.preventDefault();
                                                                    event.stopPropagation();
                                                                    handleSave();
                                                                }}
                                                            >
                                                                <SaveIcon />
                                                            </button>
                                                            <button
                                                                type="button"
                                                                className="fw-tbl-action-btn fw-tbl-action-btn--delete"
                                                                title="Delete config"
                                                                aria-label="Delete config"
                                                                disabled={!current || currentHasLinkedAcls}
                                                                onClick={(event) => {
                                                                    event.preventDefault();
                                                                    event.stopPropagation();
                                                                    setDeleteConfigOpen(true);
                                                                }}
                                                            >
                                                                <TrashIcon />
                                                            </button>
                                                        </div>
                                                    </div>
                                                </summary>
                                                <div className="fwstate-config-details__content">
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
                                            </details>
                                        </div>
                                    );
                                })()
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
                    setActiveConfig(name);
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
