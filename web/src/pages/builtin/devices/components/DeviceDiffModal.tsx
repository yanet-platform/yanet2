import React from 'react';
import * as yaml from 'js-yaml';
import type { LocalDevice } from '../types';
import { DiffModal as SharedDiffModal } from '../../_shared/DiffModal';

export interface DeviceDiffModalProps {
    device: LocalDevice;
    serverDevice: LocalDevice | null;
    onClose: () => void;
    onApply: () => Promise<void>;
}

interface DeviceYaml {
    name: string;
    type: string;
    vlan_id?: number;
    input_pipelines: { name: string; weight: number }[];
    output_pipelines: { name: string; weight: number }[];
}

const toYaml = (device: LocalDevice): string => {
    const obj: DeviceYaml = {
        name: device.id.name || '',
        type: device.type,
        input_pipelines: device.inputPipelines.map(p => ({ name: p.name || '', weight: typeof p.weight === 'number' ? p.weight : parseInt(String(p.weight), 10) || 0 })),
        output_pipelines: device.outputPipelines.map(p => ({ name: p.name || '', weight: typeof p.weight === 'number' ? p.weight : parseInt(String(p.weight), 10) || 0 })),
    };
    if (device.type === 'vlan') {
        obj.vlan_id = device.vlanId ?? 0;
    }
    return yaml.dump(obj, { sortKeys: false, lineWidth: 120, noRefs: true });
};

/** Modal showing a side-by-side YAML diff of server vs local device edits. */
export const DeviceDiffModal: React.FC<DeviceDiffModalProps> = ({
    device,
    serverDevice,
    onClose,
    onApply,
}) => (
    <SharedDiffModal
        entity={device}
        serverEntity={serverDevice}
        toYaml={toYaml}
        title={'Review changes — ' + (device.id.name || '')}
        onApply={onApply}
        onClose={onClose}
    />
);
