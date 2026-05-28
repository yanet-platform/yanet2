import React, { useState, useCallback, useRef, useMemo, useEffect } from 'react';
import { Button, Flex, Icon, Text } from '@gravity-ui/uikit';
import { ArrowDownToLine, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader } from '../../../components';
import { toaster } from '../../../utils';
import {
    usePdumpConfigs,
    usePdumpCapture,
    useConfigPackets,
} from './hooks';
import { ConfigDialog } from './ConfigDialog';
import { PacketTable } from './PacketTable';
import PdumpConfigTabs from './PdumpConfigTabs';
import FilterRow from './FilterRow';
import ConfigStrip from './ConfigStrip';
import PacketDrawer from './PacketDrawer';
import DeleteConfigDialog from './DeleteConfigDialog';
import type { PdumpConfigInfo, CapturedPacket } from './types';
import '../../../styles/draft-page.scss';
import './pdump.scss';

const NEW_PACKET_TTL_MS = 1200;
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

    const [activeConfig, setActiveConfig] = useState<string>('');
    const [editingConfig, setEditingConfig] = useState<PdumpConfigInfo | null>(null);
    const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);
    const [deletingConfigName, setDeletingConfigName] = useState<string | null>(null);
    const [isDeleting, setIsDeleting] = useState(false);
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

    const currentConfig = activeConfig || configs[0]?.name || '';

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
        setActiveConfig(name);
        setPinnedPacket(null);
        setDrawerOpen(false);
        restorePreInspectState();
        clearNewPacketState();
    }, [clearNewPacketState, restorePreInspectState]);

    const handleOpenDeleteDialog = useCallback((configName: string) => {
        setDeletingConfigName(configName);
    }, []);

    const handleCloseDeleteDialog = useCallback(() => {
        setDeletingConfigName(null);
    }, []);

    const handleConfirmDelete = useCallback(async () => {
        if (!deletingConfigName) return;
        setIsDeleting(true);
        try {
            await deleteConfig(deletingConfigName);
            if (activeConfig === deletingConfigName) {
                setActiveConfig('');
            }
            setDeletingConfigName(null);
        } finally {
            setIsDeleting(false);
        }
    }, [deletingConfigName, deleteConfig, activeConfig]);

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

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">Pdump</Text>
            <Flex grow />
            <Button view="normal" onClick={handleExportPcap} disabled={packets.length === 0}>
                <Icon data={ArrowDownToLine} size={16} />
                Export PCAP
            </Button>
            <Button view="action" onClick={() => setIsCreateDialogOpen(true)}>
                <Icon data={Plus} size={16} />
                New Configuration
            </Button>
        </Flex>
    );

    if (loading) {
        return (
            <PageLayout header={pageHeader}>
                <PageLoader loading size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={pageHeader}>
            <div className="fw-page pdump-page">
                {configs.length === 0 ? (
                    <div className="fw-empty-page">
                        <div className="fw-empty-page__message">
                            No pdump configurations found.
                        </div>
                        <Button view="action" onClick={() => setIsCreateDialogOpen(true)}>
                            New Configuration
                        </Button>
                    </div>
                ) : (
                    <>
                        <PdumpConfigTabs
                            configs={configs.map(c => c.name)}
                            activeConfig={currentConfig}
                            counts={packetCounts}
                            liveConfig={capture.liveConfig}
                            onSelect={handleSelectTab}
                            onAddConfig={() => setIsCreateDialogOpen(true)}
                        />

                        <div className="fw-content pdump-page__content">
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
                                    packets={packets}
                                    isCapturing={capture.liveConfig === currentConfig}
                                    configName={currentConfig || null}
                                    selectedPacketId={pinnedPacket?.id ?? null}
                                    onSelectPacket={handleSelectPacket}
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
