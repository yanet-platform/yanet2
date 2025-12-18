import React, { useState, useCallback } from 'react';
import { Box, Flex } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState } from '../components';
import {
    usePdumpConfigs,
    usePdumpCapture,
    ConfigCard,
    ConfigDialog,
    PacketTable,
    PacketDetailsDialog,
    PdumpPageHeader,
    type PdumpConfigInfo,
} from './pdump';

const PdumpPage: React.FC = () => {
    const { configs, loading, refetch } = usePdumpConfigs();
    const capture = usePdumpCapture();

    const [editingConfig, setEditingConfig] = useState<PdumpConfigInfo | null>(null);
    const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);
    const [selectedPacketId, setSelectedPacketId] = useState<number | null>(null);

    const handleStartCapture = useCallback((configName: string) => {
        setSelectedPacketId(null);
        capture.startCapture(configName);
    }, [capture]);

    const handleStopCapture = useCallback(() => {
        capture.stopCapture();
    }, [capture]);

    const handleClearPackets = useCallback(() => {
        setSelectedPacketId(null);
        capture.clearPackets();
    }, [capture]);

    const handleEditConfig = useCallback((config: PdumpConfigInfo) => {
        setEditingConfig(config);
    }, []);

    const handleCloseEditDialog = useCallback(() => {
        setEditingConfig(null);
    }, []);

    const handleOpenCreateDialog = useCallback(() => {
        setIsCreateDialogOpen(true);
    }, []);

    const handleCloseCreateDialog = useCallback(() => {
        setIsCreateDialogOpen(false);
    }, []);

    const handleConfigSaved = useCallback(() => {
        refetch();
    }, [refetch]);

    const handleClosePacketDetails = useCallback(() => {
        setSelectedPacketId(null);
    }, []);

    const selectedPacket = selectedPacketId !== null
        ? capture.packets.find(p => p.id === selectedPacketId) ?? null
        : null;

    const headerContent = (
        <PdumpPageHeader onCreateConfig={handleOpenCreateDialog} />
    );

    if (loading) {
        return (
            <PageLayout title="Pdump">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    if (configs.length === 0) {
        return (
            <PageLayout header={headerContent}>
                <Box style={{ width: '100%', flex: 1, minWidth: 0, padding: '20px' }}>
                    <EmptyState message="No pdump configurations found. Click 'New Configuration' to create one." />
                </Box>

                <ConfigDialog
                    open={isCreateDialogOpen}
                    onClose={handleCloseCreateDialog}
                    onSaved={handleConfigSaved}
                    isCreate
                />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Box style={{
                width: '100%',
                flex: 1,
                minWidth: 0,
                padding: '20px',
                display: 'flex',
                flexDirection: 'column',
                overflow: 'hidden',
            }}>
                {/* Configs section */}
                <Box
                    style={{
                        paddingBottom: '12px',
                        borderBottom: '1px solid var(--g-color-line-generic)',
                        flexShrink: 0,
                    }}
                >
                    <Flex gap={3} style={{ flexWrap: 'wrap' }}>
                        {configs.map((config) => (
                            <ConfigCard
                                key={config.name}
                                config={config}
                                isCapturing={capture.isCapturing}
                                isCaptureActive={
                                    capture.isCapturing && capture.configName === config.name
                                }
                                onStartCapture={() => handleStartCapture(config.name)}
                                onStopCapture={handleStopCapture}
                                onEdit={() => handleEditConfig(config)}
                            />
                        ))}
                    </Flex>
                </Box>

                {/* Packet table - full width */}
                <Box style={{ flex: 1, minHeight: 0, marginTop: '16px' }}>
                    <PacketTable
                        packets={capture.packets}
                        isCapturing={capture.isCapturing}
                        configName={capture.configName}
                        selectedPacketId={selectedPacketId}
                        onSelectPacket={setSelectedPacketId}
                        onStopCapture={handleStopCapture}
                        onClearPackets={handleClearPackets}
                    />
                </Box>
            </Box>

            {/* Packet details dialog */}
            <PacketDetailsDialog
                packet={selectedPacket}
                open={selectedPacketId !== null && selectedPacket !== null}
                onClose={handleClosePacketDetails}
            />

            {/* Create config dialog */}
            <ConfigDialog
                open={isCreateDialogOpen}
                onClose={handleCloseCreateDialog}
                onSaved={handleConfigSaved}
                isCreate
            />

            {/* Edit config dialog */}
            {editingConfig && (
                <ConfigDialog
                    open={true}
                    onClose={handleCloseEditDialog}
                    configName={editingConfig.name}
                    initialConfig={editingConfig.config}
                    onSaved={handleConfigSaved}
                />
            )}
        </PageLayout>
    );
};

export default PdumpPage;
