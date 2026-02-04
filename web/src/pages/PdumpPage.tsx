import React, { useState, useCallback } from 'react';
import { Box, Flex } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState } from '../components';
import {
    usePdumpConfigs,
    usePdumpCapture,
    ConfigCard,
    ConfigDialog,
    DeleteConfigDialog,
    PacketTable,
    PacketDetailsDialog,
    PdumpPageHeader,
    type PdumpConfigInfo,
} from './pdump';
import './pdump/pdump.scss';

const PdumpPage: React.FC = () => {
    const { configs, loading, refetch, deleteConfig } = usePdumpConfigs();
    const capture = usePdumpCapture();

    const [editingConfig, setEditingConfig] = useState<PdumpConfigInfo | null>(null);
    const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);
    const [deletingConfigName, setDeletingConfigName] = useState<string | null>(null);
    const [isDeleting, setIsDeleting] = useState(false);
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
            setDeletingConfigName(null);
        } finally {
            setIsDeleting(false);
        }
    }, [deletingConfigName, deleteConfig]);

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
                <Box className="pdump-page__empty">
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
            <Box className="pdump-page__content">
                {/* Configs section */}
                <Box className="pdump-page__configs-section">
                    <Flex gap={3} className="pdump-page__configs-list">
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
                                onDelete={() => handleOpenDeleteDialog(config.name)}
                            />
                        ))}
                    </Flex>
                </Box>

                {/* Packet table - full width */}
                <Box className="pdump-page__table-section">
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

            {/* Delete config confirmation dialog */}
            <DeleteConfigDialog
                open={deletingConfigName !== null}
                onClose={handleCloseDeleteDialog}
                onConfirm={handleConfirmDelete}
                configName={deletingConfigName ?? ''}
                loading={isDeleting}
            />
        </PageLayout>
    );
};

export default PdumpPage;
