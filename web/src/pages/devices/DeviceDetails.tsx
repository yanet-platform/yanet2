import React, { useState, useCallback, useEffect } from 'react';
import { Box, Text, Flex, TextInput, Label, Button, Icon } from '@gravity-ui/uikit';
import { FloppyDisk, PlugConnection, Layers } from '@gravity-ui/icons';
import type { PipelineId } from '../../api/pipelines';
import type { DevicePipeline } from '../../api/devices';
import { PipelineTable } from './PipelineTable';
import type { LocalDevice } from './types';
import './devices.scss';

export interface DeviceDetailsProps {
    device: LocalDevice | null;
    loadPipelineList: () => Promise<PipelineId[]>;
    onUpdate: (updates: Partial<LocalDevice>) => void;
    onSave: () => Promise<boolean>;
}

export const DeviceDetails: React.FC<DeviceDetailsProps> = ({
    device,
    loadPipelineList,
    onUpdate,
    onSave,
}) => {
    const [saving, setSaving] = useState(false);
    const [availablePipelines, setAvailablePipelines] = useState<PipelineId[]>([]);
    const [loadingPipelines, setLoadingPipelines] = useState(false);

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

    if (!device) {
        return (
            <Box className="device-details device-details__empty">
                <Text variant="body-1" color="secondary">
                    Select a device to view details
                </Text>
            </Box>
        );
    }

    return (
        <Box className="device-details">
            <Box className="device-details__header">
                <Flex alignItems="center" gap={2}>
                    <Icon
                        data={device.type === 'vlan' ? Layers : PlugConnection}
                        size={18}
                        className={`device-details__type-icon device-details__type-icon--${device.type === 'vlan' ? 'vlan' : 'port'}`}
                    />
                    <Text variant="subheader-2">{device.id.name}</Text>
                    {device.isNew && <Label theme="warning">new</Label>}
                    {device.isDirty && !device.isNew && (
                        <Text variant="caption-1" color="secondary">
                            (unsaved changes)
                        </Text>
                    )}
                </Flex>
                <Button
                    view="action"
                    onClick={handleSave}
                    disabled={!device.isDirty}
                    loading={saving}
                >
                    <Button.Icon>
                        <FloppyDisk />
                    </Button.Icon>
                    Save
                </Button>
            </Box>

            <Box className="device-details__body">
                {device.type === 'vlan' && (
                    <Flex alignItems="center" gap={2} className="device-details__vlan-row">
                        <Text variant="body-1">VLAN ID:</Text>
                        <TextInput
                            value={String(device.vlanId ?? 0)}
                            onChange={handleVlanIdChange}
                            type="number"
                            style={{ width: '100px' }}
                        />
                    </Flex>
                )}

                <Flex gap={4} className="device-details__pipelines">
                    <Box className="device-details__pipeline-col">
                        <PipelineTable
                            pipelineLabel="RX Pipeline"
                            pipelines={device.inputPipelines}
                            availablePipelines={availablePipelines}
                            loadingPipelines={loadingPipelines}
                            onChange={handleInputPipelinesChange}
                        />
                    </Box>
                    <Box className="device-details__pipeline-col">
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
    );
};

