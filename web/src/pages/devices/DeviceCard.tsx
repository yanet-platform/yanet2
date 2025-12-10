import React, { useState, useCallback, useEffect } from 'react';
import { Box, Text, Flex, Card, TextInput, Label } from '@gravity-ui/uikit';
import type { PipelineId } from '../../api/pipelines';
import type { DevicePipeline } from '../../api/devices';
import { CardHeader } from '../../components';
import { PipelineTable } from './PipelineTable';
import type { LocalDevice } from './types';

export interface DeviceCardProps {
    device: LocalDevice;
    loadPipelineList: () => Promise<PipelineId[]>;
    onUpdate: (updates: Partial<LocalDevice>) => void;
    onSave: () => Promise<boolean>;
}

export const DeviceCard: React.FC<DeviceCardProps> = ({
    device,
    loadPipelineList,
    onUpdate,
    onSave,
}) => {
    const [saving, setSaving] = useState(false);
    const [availablePipelines, setAvailablePipelines] = useState<PipelineId[]>([]);
    const [loadingPipelines, setLoadingPipelines] = useState(false);

    // Load available pipelines on mount
    useEffect(() => {
        const load = async () => {
            setLoadingPipelines(true);
            const pipelines = await loadPipelineList();
            setAvailablePipelines(pipelines);
            setLoadingPipelines(false);
        };
        load();
    }, [loadPipelineList]);

    const handleSave = useCallback(async () => {
        setSaving(true);
        await onSave();
        setSaving(false);
    }, [onSave]);

    const handleInputPipelinesChange = useCallback((pipelines: DevicePipeline[]) => {
        onUpdate({ inputPipelines: pipelines });
    }, [onUpdate]);

    const handleOutputPipelinesChange = useCallback((pipelines: DevicePipeline[]) => {
        onUpdate({ outputPipelines: pipelines });
    }, [onUpdate]);

    const handleVlanIdChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        const value = parseInt(e.target.value, 10);
        onUpdate({ vlanId: isNaN(value) ? 0 : value });
    }, [onUpdate]);

    const typeLabel = (
        <Label theme={device.type === 'vlan' ? 'info' : 'normal'}>
            {device.type}
        </Label>
    );

    return (
        <Card style={{ marginBottom: '16px' }}>
            <Box style={{ display: 'flex', flexDirection: 'column' }}>
                <CardHeader
                    title={device.id.name || ''}
                    isDirty={device.isDirty}
                    isNew={device.isNew}
                    onSave={handleSave}
                    saving={saving}
                    labels={typeLabel}
                />

                {/* Content */}
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
                    {/* VLAN ID field for vlan devices */}
                    {device.type === 'vlan' && (
                        <Flex alignItems="center" gap={2}>
                            <Text variant="body-1">VLAN ID:</Text>
                            <TextInput
                                value={String(device.vlanId ?? 0)}
                                onChange={handleVlanIdChange}
                                type="number"
                                style={{ width: '100px' }}
                            />
                        </Flex>
                    )}

                    {/* Pipeline tables */}
                    <Flex gap={4} className="pipelineTables" style={{ flexWrap: 'wrap' }}>
                        <Box style={{ flex: 1, minWidth: '300px' }}>
                            <PipelineTable
                                pipelineLabel="RX Pipeline"
                                pipelines={device.inputPipelines}
                                availablePipelines={availablePipelines}
                                loadingPipelines={loadingPipelines}
                                onChange={handleInputPipelinesChange}
                            />
                        </Box>
                        <Box style={{ flex: 1, minWidth: '300px' }}>
                            <PipelineTable
                                pipelineLabel="TX Pipeline"
                                pipelines={device.outputPipelines}
                                availablePipelines={availablePipelines}
                                loadingPipelines={loadingPipelines}
                                onChange={handleOutputPipelinesChange}
                            />
                        </Box>
                    </Flex>
                </Box>
            </Box>
        </Card>
    );
};
