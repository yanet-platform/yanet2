import { devices, toDevicePayload } from '@yanet/core/api';
import type { BaseDevice, DeviceTypeManifest } from '@yanet/core/registry';
import { IconPlain } from './icon';

const save = async (device: BaseDevice): Promise<Record<string, unknown>> => {
    const response = await devices.updatePlain({
        name: device.id.name,
        device: toDevicePayload(device.inputPipelines, device.outputPipelines),
    });
    if (response.error) {
        throw new Error(response.error);
    }
    return {};
};

export const deviceType: DeviceTypeManifest = {
    type: 'plain',
    label: 'Physical',
    pluralLabel: 'Physical',
    navOrder: 10,
    icon: IconPlain,
    accentColor: 'var(--teal)',
    kindTag: () => 'PHYSICAL',
    typeDescription: 'physical (plain)',
    trafficSource: true,
    rowSubtitle: () => '— · —',
    parentMode: 'instances',
    propertyRows: () => [
        { label: 'Driver', value: '—', mono: true },
        { label: 'PCI bus', value: '—', mono: true },
    ],
    save,
};
