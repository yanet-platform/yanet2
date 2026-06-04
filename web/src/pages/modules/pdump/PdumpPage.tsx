import React, { useState, useCallback, useRef, useMemo, useEffect } from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { ArrowDownToLine, Plus } from '@gravity-ui/icons';
import { useSearchParams } from 'react-router-dom';
import { useSearchParamHelpers } from '../../../hooks';
import { PageLayout, PageLoader, ConfigTabStrip, EmptyPagePlaceholder, CommandPaletteHeader } from '../../../components';
import { toaster } from '../../../utils';
import {
    usePdumpConfigs,
    usePdumpCapture,
    useConfigPackets,
} from './hooks';
import { ConfigDialog } from './ConfigDialog';
import { PacketTable } from './PacketTable';
import FilterRow from './FilterRow';
import ConfigStrip from './ConfigStrip';
import PacketDrawer from './PacketDrawer';
import DeleteConfigDialog from './DeleteConfigDialog';
import type { PdumpConfigInfo, CapturedPacket } from './types';
import { usePalette } from '../../_shared/command-palette';
import type { Command } from '../../_shared/command-palette';
import { useTabCycle } from '../../_shared/useTabCycle';
import '../../../styles/draft-page.scss';
import './pdump.scss';

const NEW_PACKET_TTL_MS = 1200;
const EMPTY_DIRTY_SET = new Set<string>();
const QP_CONFIG = 'config';
const QP_SEARCH = 'search';
const EMPTY_PPS_HISTORY: number[] = [];
const PCAP_GLOBAL_HEADER_BYTES = 24;
const PCAP_PACKET_HEADER_BYTES = 16;
const PCAP_LINKTYPE_ETHERNET = 1;

const sanitizeFilenamePart = (value: string): string => {
    const sanitized = value.replace(/[^a-zA-Z0-9._-]/g, '-').replace(/-+/g, '-').replace(/^-|-$/g, '');
    return sanitized || 'capture';
};

const createPcapBuffer = (records: CapturedPacket[]): ArrayBuffer => {
    let totalSize = PCAP_GLOBAL_HEADER_BYTES;
    for (const packet of records) {
        totalSize += PCAP_PACKET_HEADER_BYTES + packet.parsed.raw.length;
    }

    const buffer = new ArrayBuffer(totalSize);
    const view = new DataView(buffer);
    const bytes = new Uint8Array(buffer);

    view.setUint32(0, 0xa1b2c3d4, true);
    view.setUint16(4, 2, true);
    view.setUint16(6, 4, true);
    view.setInt32(8, 0, true);
    view.setUint32(12, 0, true);
    view.setUint32(16, 65535, true);
    view.setUint32(20, PCAP_LINKTYPE_ETHERNET, true);

    let offset = PCAP_GLOBAL_HEADER_BYTES;
    for (const packet of records) {
        const payload = packet.parsed.raw;
        const capturedLength = payload.length;
        const originalLength = packet.record.meta?.packet_len ?? capturedLength;
        const timestampMs = packet.timestamp.getTime();
        const tsSec = Math.floor(timestampMs / 1000);
        const tsUsec = Math.floor((timestampMs % 1000) * 1000);

        view.setUint32(offset, tsSec, true);
        view.setUint32(offset + 4, tsUsec, true);
        view.setUint32(offset + 8, capturedLength, true);
        view.setUint32(offset + 12, originalLength, true);
        offset += PCAP_PACKET_HEADER_BYTES;

        bytes.set(payload, offset);
        offset += capturedLength;
    }

    return buffer;
};

const PdumpPage: React.FC = () => {
    const { configs, loading, refetch, deleteConfig } = usePdumpConfigs();
    const [searchParams, setSearchParams] = useSearchParams();
    const queryConfig = useMemo(() => searchParams.get(QP_CONFIG), [searchParams]);
    const searchQuery = useMemo(() => searchParams.get(QP_SEARCH) || '', [searchParams]);
    const [editingConfig, setEditingConfig] = useState<PdumpConfigInfo | null>(null);
    const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);
    const [deletingConfigName, setDeletingConfigName] = useState<string | null>(null);
    const [isDeleting, setIsDeleting] = useState(false);
    const [deleteInFlightConfig, setDeleteInFlightConfig] = useState<string | null>(null);
    const [excludedConfigNames, setExcludedConfigNames] = useState<Set<string>>(new Set());
    const [paused, setPaused] = useState(false);
    const [autoScroll, setAutoScroll] = useState(true);
    const [pinnedPacket, setPinnedPacket] = useState<CapturedPacket | null>(null);
    const [drawerOpen, setDrawerOpen] = useState(false);
    const preInspectStateRef = useRef<{ autoScroll: boolean; paused: boolean } | null>(null);

    const capture = usePdumpCapture(paused);

    const [newPacketIds, setNewPacketIds] = useState<Set<number>>(new Set());
    const maxSeenIdRef = useRef<number>(-1);
    const flashTimerRef = useRef<number | null>(null);

    const clearNewPacketState = useCallback(() => {
        if (flashTimerRef.current !== null) {
            clearTimeout(flashTimerRef.current);
            flashTimerRef.current = null;
        }
        maxSeenIdRef.current = -1;
        setNewPacketIds(new Set());
    }, []);

    const { updateParams: updateSearchParams, clearConfigParamIfCurrent } = useSearchParamHelpers(setSearchParams, QP_CONFIG);

    const handleTogglePause = useCallback(() => {
        setPaused(prev => {
            const next = !prev;
            if (next) {
                if (flashTimerRef.current !== null) {
                    clearTimeout(flashTimerRef.current);
                    flashTimerRef.current = null;
                }
                setNewPacketIds(new Set());
            }
            return next;
        });
    }, []);

    const availableConfigNames = useMemo(() => (
        configs
            .map(({ name }) => name)
            .filter(name => !excludedConfigNames.has(name))
    ), [configs, excludedConfigNames]);
    const hasAvailableConfigs = availableConfigNames.length > 0;
    const currentConfig = useMemo(() => {
        if (!loading) {
            if (
                queryConfig
                && (
                    availableConfigNames.includes(queryConfig)
                    || queryConfig === deleteInFlightConfig
                )
            ) {
                return queryConfig;
            }
            if (queryConfig && !hasAvailableConfigs) {
                return '';
            }
            return hasAvailableConfigs ? availableConfigNames[0] || '' : '';
        }
        return queryConfig || (hasAvailableConfigs ? availableConfigNames[0] || '' : '');
    }, [availableConfigNames, deleteInFlightConfig, hasAvailableConfigs, loading, queryConfig]);

    useEffect(() => {
        const updates: Record<string, string | null> = {};
        if (!loading) {
            if (!hasAvailableConfigs) {
                if (searchParams.get(QP_CONFIG) !== null) {
                    updates[QP_CONFIG] = null;
                }
            } else if (queryConfig !== currentConfig) {
                updates[QP_CONFIG] = currentConfig || null;
            }
        }
        if (Object.keys(updates).length > 0) {
            updateSearchParams(updates);
        }
    }, [currentConfig, hasAvailableConfigs, loading, queryConfig, searchParams, updateSearchParams]);

    useEffect(() => {
        if (excludedConfigNames.size === 0) {
            return;
        }

        setExcludedConfigNames(prev => {
            const next = new Set(prev);
            let changed = false;

            for (const name of prev) {
                if (!configs.some(config => config.name === name)) {
                    next.delete(name);
                    changed = true;
                }
            }

            return changed ? next : prev;
        });
    }, [configs, excludedConfigNames]);

    const currentConfigInfo = useMemo(
        () => configs.find(c => c.name === currentConfig) ?? null,
        [configs, currentConfig]
    );

    const packets = useConfigPackets(capture.packetsByConfig, currentConfig);
    const ppsHistory = capture.ppsByConfig[currentConfig] ?? EMPTY_PPS_HISTORY;

    // Refs used to stabilise nav callbacks so their identity does not change on
    // every flush (packets changes each flush; pinnedPacket changes on selection).
    const packetsRef = useRef(packets);
    useEffect(() => { packetsRef.current = packets; }, [packets]);

    const pinnedPacketRef = useRef(pinnedPacket);
    useEffect(() => { pinnedPacketRef.current = pinnedPacket; }, [pinnedPacket]);

    const packetCounts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        configs.forEach(c => {
            m.set(c.name, (capture.packetsByConfig[c.name] ?? []).length);
        });
        return m;
    }, [configs, capture.packetsByConfig]);

    useEffect(() => {
        return () => {
            if (flashTimerRef.current !== null) clearTimeout(flashTimerRef.current);
        };
    }, []);

    useEffect(() => {
        if (packets.length === 0) return;

        if (paused) {
            const last = packets[packets.length - 1];
            if (last && last.id > maxSeenIdRef.current) {
                maxSeenIdRef.current = last.id;
            }
            return;
        }

        const newIds: number[] = [];
        for (let idx = packets.length - 1; idx >= 0; idx--) {
            const p = packets[idx];
            if (!p || p.id <= maxSeenIdRef.current) break;
            newIds.push(p.id);
        }
        if (newIds.length === 0) return;
        let max = maxSeenIdRef.current;
        for (const id of newIds) {
            if (id > max) max = id;
        }
        maxSeenIdRef.current = max;
        setNewPacketIds(prev => {
            const next = new Set(prev);
            for (const id of newIds) next.add(id);
            return next;
        });
        if (flashTimerRef.current !== null) clearTimeout(flashTimerRef.current);
        flashTimerRef.current = window.setTimeout(() => {
            flashTimerRef.current = null;
            setNewPacketIds(new Set());
        }, NEW_PACKET_TTL_MS);
    }, [packets, paused]);

    const selectedPacketIndex = pinnedPacket
        ? packets.findIndex(p => p.id === pinnedPacket.id)
        : -1;

    const restorePreInspectState = useCallback(() => {
        if (preInspectStateRef.current) {
            setAutoScroll(preInspectStateRef.current.autoScroll);
            setPaused(preInspectStateRef.current.paused);
            preInspectStateRef.current = null;
        }
    }, []);

    useEffect(() => {
        setPinnedPacket(null);
        setDrawerOpen(false);
        restorePreInspectState();
        clearNewPacketState();
    }, [currentConfig, clearNewPacketState, restorePreInspectState]);

    const handleSelectPacket = useCallback((packet: CapturedPacket | null) => {
        if (packet) {
            if (preInspectStateRef.current === null) {
                preInspectStateRef.current = { autoScroll, paused };
            }
            setPinnedPacket(packet);
            setDrawerOpen(true);
            setAutoScroll(false);
            setPaused(true);
        } else {
            setPinnedPacket(null);
            setDrawerOpen(false);
            restorePreInspectState();
        }
    }, [autoScroll, paused, restorePreInspectState]);

    const handleCloseDrawer = useCallback(() => {
        handleSelectPacket(null);
    }, [handleSelectPacket]);

    // Stable forever: reads current packets/pinnedPacket via refs so callback
    // identity does not change each flush. PacketDrawer receives the same function
    // reference until the component unmounts.
    const handlePrevPacket = useCallback(() => {
        const pinned = pinnedPacketRef.current;
        if (!pinned) return;
        const cur = packetsRef.current;
        const idx = cur.findIndex(p => p.id === pinned.id);
        if (idx > 0) {
            const prev = cur[idx - 1];
            if (prev) setPinnedPacket(prev);
        }
    }, []);

    const handleNextPacket = useCallback(() => {
        const pinned = pinnedPacketRef.current;
        if (!pinned) return;
        const cur = packetsRef.current;
        const idx = cur.findIndex(p => p.id === pinned.id);
        if (idx >= 0 && idx < cur.length - 1) {
            const next = cur[idx + 1];
            if (next) setPinnedPacket(next);
        }
    }, []);

    const handleStartCapture = useCallback((configName: string) => {
        setPinnedPacket(null);
        setDrawerOpen(false);
        restorePreInspectState();
        clearNewPacketState();
        capture.startCapture(configName);
    }, [capture, clearNewPacketState, restorePreInspectState]);

    const handleStopCapture = useCallback(() => {
        capture.stopCapture();
    }, [capture]);

    const handleClearPackets = useCallback(() => {
        setPinnedPacket(null);
        setDrawerOpen(false);
        restorePreInspectState();
        clearNewPacketState();
        capture.clearPackets(currentConfig);
    }, [capture, currentConfig, clearNewPacketState, restorePreInspectState]);

    const handleConfigSaved = useCallback(() => {
        refetch();
    }, [refetch]);

    const handleSelectTab = useCallback((name: string) => {
        updateSearchParams({ [QP_CONFIG]: name || null });
    }, [updateSearchParams]);

    useTabCycle({
        tabs: configs.map((c) => c.name),
        activeTab: currentConfig,
        onSelect: handleSelectTab,
        enabled: !loading,
    });

    const { setPageContribution } = usePalette();

    const handleOpenDeleteDialog = useCallback((configName: string) => {
        setDeletingConfigName(configName);
    }, []);

    const handleCloseDeleteDialog = useCallback(() => {
        if (isDeleting) {
            return;
        }
        setDeletingConfigName(null);
    }, [isDeleting]);

    const handleConfirmDelete = useCallback(async () => {
        if (!deletingConfigName) return;
        setIsDeleting(true);
        setDeleteInFlightConfig(deletingConfigName);
        try {
            const deleted = await deleteConfig(deletingConfigName);
            if (deleted) {
                setExcludedConfigNames(prev => {
                    const next = new Set(prev);
                    next.add(deletingConfigName);
                    return next;
                });
                clearConfigParamIfCurrent(deletingConfigName);
                setDeletingConfigName(null);
            }
        } finally {
            setDeleteInFlightConfig(null);
            setIsDeleting(false);
        }
    }, [clearConfigParamIfCurrent, deleteConfig, deletingConfigName]);

    const handleSearchChange = useCallback((value: string): void => {
        updateSearchParams({ [QP_SEARCH]: value || null });
    }, [updateSearchParams]);

    const handleExportPcap = useCallback(() => {
        if (packets.length === 0) {
            return;
        }

        try {
            const pcapData = createPcapBuffer(packets);
            const blob = new Blob([pcapData], { type: 'application/vnd.tcpdump.pcap' });
            const url = URL.createObjectURL(blob);
            const link = document.createElement('a');
            link.href = url;
            link.download = `pdump-${sanitizeFilenamePart(currentConfig || 'capture')}.pcap`;
            link.click();
            URL.revokeObjectURL(url);
            toaster.success('pdump-export-pcap', 'PCAP export started.');
        } catch (error) {
            const message = error instanceof Error ? error.message : String(error);
            toaster.error('pdump-export-pcap-error', `Failed to export PCAP: ${message}`);
        }
    }, [currentConfig, packets]);

    const commands = useMemo((): Command[] => {
        const list: Command[] = [];

        if (currentConfigInfo && !capture.isCapturing) {
            list.push({
                id: '__start_capture',
                icon: '▶',
                label: 'Start capture',
                sub: `Start capturing on "${currentConfig}"`,
                keywords: 'start capture begin record',
                group: 'Capture',
                onSelect: () => handleStartCapture(currentConfig),
            });
        }
        if (capture.liveConfig === currentConfig) {
            list.push({
                id: '__stop_capture',
                icon: '■',
                label: 'Stop capture',
                sub: `Stop capturing on "${currentConfig}"`,
                keywords: 'stop capture end halt',
                group: 'Capture',
                onSelect: handleStopCapture,
            });
        }
        if (currentConfig) {
            list.push({
                id: '__toggle_pause',
                icon: paused ? '▶' : '⏸',
                label: paused ? 'Resume stream' : 'Pause stream',
                sub: paused ? 'Resume live packet stream' : 'Pause live packet stream',
                keywords: 'pause resume stream toggle',
                group: 'Capture',
                onSelect: handleTogglePause,
            });
        }
        if (packets.length > 0) {
            list.push({
                id: '__clear_packets',
                icon: '✕',
                label: 'Clear packets',
                sub: 'Remove all captured packets',
                keywords: 'clear packets remove flush',
                group: 'Capture',
                onSelect: handleClearPackets,
            });
        }
        if (currentConfig) {
            list.push({
                id: '__toggle_autoscroll',
                icon: '↧',
                label: autoScroll ? 'Disable auto-scroll' : 'Enable auto-scroll',
                sub: autoScroll ? 'Stop following new packets' : 'Follow new packets',
                keywords: 'auto scroll follow tail',
                group: 'Capture',
                onSelect: () => setAutoScroll(v => !v),
            });
        }

        list.push({
            id: '__new_config',
            icon: '+',
            label: 'New configuration',
            sub: 'Create a new pdump configuration',
            keywords: 'new config create add',
            group: 'Config',
            onSelect: () => setIsCreateDialogOpen(true),
        });
        if (currentConfigInfo) {
            list.push({
                id: '__edit_config',
                icon: '✎',
                label: 'Edit configuration',
                sub: `Edit "${currentConfig}"`,
                keywords: 'edit config modify',
                group: 'Config',
                onSelect: () => setEditingConfig(currentConfigInfo),
            });
        }
        if (currentConfig && capture.liveConfig !== currentConfig) {
            list.push({
                id: '__delete_config',
                icon: '✕',
                label: 'Delete configuration',
                sub: `Delete "${currentConfig}"`,
                keywords: 'delete remove config',
                group: 'Config',
                onSelect: () => handleOpenDeleteDialog(currentConfig),
            });
        }
        for (const cfg of configs) {
            if (cfg.name === currentConfig) continue;
            const name = cfg.name;
            list.push({
                id: `__config_${name}`,
                icon: '⇥',
                label: `Switch to config ${name}`,
                sub: name,
                keywords: `switch config tab ${name}`,
                group: 'Config',
                onSelect: () => handleSelectTab(name),
            });
        }

        return list;
    }, [
        currentConfig,
        currentConfigInfo,
        configs,
        capture.liveConfig,
        capture.isCapturing,
        paused,
        autoScroll,
        packets.length,
        handleStartCapture,
        handleStopCapture,
        handleTogglePause,
        handleClearPackets,
        handleSelectTab,
        handleOpenDeleteDialog,
    ]);

    useEffect(() => {
        setPageContribution({ commands, placeholder: 'Search pdump actions…' });
        return () => setPageContribution(null);
    }, [commands, setPageContribution]);

    const pageHeader = (
        <CommandPaletteHeader
            title="Pdump"
            placeholder="Search pdump actions…"
            actions={<>
                <Button view="normal" onClick={handleExportPcap} disabled={packets.length === 0}>
                    <Icon data={ArrowDownToLine} size={16} />
                    Export PCAP
                </Button>
                <Button view="action" onClick={() => setIsCreateDialogOpen(true)}>
                    <Icon data={Plus} size={16} />
                    New Configuration
                </Button>
            </>}
        />
    );

    if (loading) {
        return (
            <PageLayout header={pageHeader} className="yn-flat-layout">
                <PageLoader loading size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={pageHeader} className="yn-flat-layout">
            <div className="yn-page pdump-page yn-flat-page">
                {configs.length === 0 ? (
                    <EmptyPagePlaceholder
                        message="No pdump configurations found."
                        actionLabel="New Configuration"
                        onAction={() => setIsCreateDialogOpen(true)}
                    />
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={configs.map(c => c.name)}
                            activeConfig={currentConfig}
                            counts={packetCounts}
                            dirtyConfigs={EMPTY_DIRTY_SET}
                            onSelect={handleSelectTab}
                            onAddConfig={() => setIsCreateDialogOpen(true)}
                            leadingIcon={(cfg) => cfg === capture.liveConfig ? <span className="yn-tab__dot yn-tab__dot--live" aria-label="live capture" /> : null}
                        />

                        <div className="yn-content pdump-page__content">
                            {currentConfigInfo && (
                                <>
                                    <FilterRow filter={currentConfigInfo.config?.filter ?? ''} />
                                    <ConfigStrip
                                        config={currentConfigInfo}
                                        isCapturing={capture.isCapturing}
                                        isCaptureActive={capture.liveConfig === currentConfig}
                                        packetCount={packets.length}
                                        ppsHistory={ppsHistory}
                                        onStartCapture={() => handleStartCapture(currentConfig)}
                                        onStopCapture={handleStopCapture}
                                        onEdit={() => setEditingConfig(currentConfigInfo)}
                                        onDelete={() => handleOpenDeleteDialog(currentConfig)}
                                    />
                                </>
                            )}

                            <div className="pdump-page__table">
                                <PacketTable
                                    key={currentConfig || 'empty'}
                                    packets={packets}
                                    isCapturing={capture.liveConfig === currentConfig}
                                    configName={currentConfig || null}
                                    searchQuery={searchQuery}
                                    selectedPacketId={pinnedPacket?.id ?? null}
                                    onSelectPacket={handleSelectPacket}
                                    onSearchQueryChange={handleSearchChange}
                                    onClearPackets={handleClearPackets}
                                    newPacketIds={newPacketIds}
                                    paused={paused}
                                    onTogglePause={handleTogglePause}
                                    autoScroll={autoScroll}
                                    onAutoScrollChange={setAutoScroll}
                                />
                            </div>
                        </div>
                    </>
                )}

                <PacketDrawer
                    open={drawerOpen}
                    packet={pinnedPacket}
                    packetIndex={selectedPacketIndex}
                    totalPackets={packets.length}
                    configName={currentConfig}
                    onClose={handleCloseDrawer}
                    onPrev={handlePrevPacket}
                    onNext={handleNextPacket}
                />
            </div>

            <ConfigDialog
                open={isCreateDialogOpen}
                onClose={() => setIsCreateDialogOpen(false)}
                onSaved={handleConfigSaved}
                isCreate
            />

            {editingConfig && (
                <ConfigDialog
                    open={true}
                    onClose={() => setEditingConfig(null)}
                    configName={editingConfig.name}
                    initialConfig={editingConfig.config}
                    onSaved={handleConfigSaved}
                />
            )}

            {deletingConfigName !== null && (
                <DeleteConfigDialog
                    name={deletingConfigName}
                    isDeleting={isDeleting}
                    onClose={handleCloseDeleteDialog}
                    onConfirm={handleConfirmDelete}
                />
            )}
        </PageLayout>
    );
};

export default PdumpPage;
