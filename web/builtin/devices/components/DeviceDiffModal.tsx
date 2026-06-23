import React from 'react';
import type { LocalDevice } from '../types';
import { SaveDiffModal } from '@yanet/core/components';
import { deviceTypeManifest } from '@yanet/core/registry';
import { dumpYamlDoc } from '@yanet/core/utils';

export interface DeviceDiffModalProps {
    device: LocalDevice;
    serverDevice: LocalDevice | null;
    onClose: () => void;
    onApply: () => Promise<void>;
}

const toYaml = (device: LocalDevice): string => {
    const obj: Record<string, unknown> = {
        name: device.id.name || '',
        type: device.type,
        input_pipelines: device.inputPipelines.map(p => ({ name: p.name || '', weight: typeof p.weight === 'number' ? p.weight : parseInt(String(p.weight), 10) || 0 })),
        output_pipelines: device.outputPipelines.map(p => ({ name: p.name || '', weight: typeof p.weight === 'number' ? p.weight : parseInt(String(p.weight), 10) || 0 })),
        ...(deviceTypeManifest(device.type)?.diffYaml?.(device) ?? {}),
    };
    return dumpYamlDoc(obj);
};

/** Modal showing a side-by-side YAML diff of server vs local device edits. */
export const DeviceDiffModal: React.FC<DeviceDiffModalProps> = ({
    device,
    serverDevice,
    onClose,
    onApply,
}) => (
    <SaveDiffModal
        configName={device.id.name || ''}
        beforeYaml={serverDevice != null ? toYaml(serverDevice) : ''}
        afterYaml={toYaml(device)}
        onApply={onApply}
        onClose={onClose}
    />
);
