import { devices, toDevicePayload } from '@yanet/core/api';
import type { BaseDevice, DeviceTypeManifest } from '@yanet/core/registry';
import { IconVlan } from './icon';

interface VlanExt {
    vlanId?: number;
}

const vlanExt = (device: BaseDevice): VlanExt =>
    (device.ext.vlan as VlanExt | undefined) ?? {};

const save = async (device: BaseDevice): Promise<Record<string, unknown>> => {
    const { vlanId } = vlanExt(device);
    const response = await devices.updateVlan({
        name: device.id.name,
        device: toDevicePayload(device.inputPipelines, device.outputPipelines),
        vlan: vlanId ?? 0,
    });
    if (response.error) {
        throw new Error(response.error);
    }
    return { vlanId: vlanId ?? 0 };
};

export const deviceType: DeviceTypeManifest = {
    type: 'vlan',
    label: 'VLAN',
    pluralLabel: 'VLAN',
    navOrder: 20,
    icon: IconVlan,
    accentColor: 'var(--violet)',
    kindTag: (device) => {
        const { vlanId } = vlanExt(device);
        return `VLAN · ${vlanId ?? '—'}`;
    },
    typeDescription: 'logical (vlan)',
    rowSubtitle: () => 'vlan · —',
    rowBadge: (device) => {
        const { vlanId } = vlanExt(device);
        return vlanId !== undefined ? String(vlanId) : undefined;
    },
    parentGroupLabel: '∅ orphan VLANs',
    propertyRows: (device) => {
        const { vlanId } = vlanExt(device);
        return [
            { label: 'VLAN ID', value: vlanId !== undefined ? String(vlanId) : '—', mono: true },
            { label: 'Parent device', value: '—', mono: true },
        ];
    },
    createDefaults: () => ({ vlanId: 0 }),
    extDirty: (device, snapshot) => {
        const current = vlanExt(device);
        const clean = snapshot ? vlanExt(snapshot) : undefined;
        return device.isNew || (clean != null && current.vlanId !== clean.vlanId);
    },
    diffYaml: (device) => ({ vlan_id: vlanExt(device).vlanId ?? 0 }),
    save,
};
