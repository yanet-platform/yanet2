import './vxlan.scss';
import { ApiError, devices, toDevicePayload } from '@yanet/core/api';
import type { BaseDevice, DeviceTypeManifest } from '@yanet/core/registry';
import { vxlanExt, isValidExt, resolvedExt, DEFAULT_DST_PORT } from './types';
import { IconVxlan } from './icon';

// The Save button stays disabled while any editor shows a value the
// control plane would reject; save itself refuses the same ext, so a
// programmatic caller cannot submit a stale last-valid number either.
const save = async (device: BaseDevice): Promise<Record<string, unknown>> => {
    if (!isValidExt(vxlanExt(device))) {
        throw new Error('tunnel settings are incomplete or invalid');
    }
    const ext = resolvedExt(device);
    const response = await devices.updateVxlan({
        name: device.id.name,
        device: toDevicePayload(device.inputPipelines, device.outputPipelines),
        vni: ext.vni,
        dst_port: ext.dstPort,
        src_mac: ext.srcMac,
        dst_mac: ext.dstMac,
        src_ip: ext.srcIp,
        dst_ip: ext.dstIp,
    });
    if (response.error) {
        throw new Error(response.error);
    }
    return { ...ext };
};

// GetDevice serves only devices configured since this control plane last
// started, so a 404 is an expected miss: the empty slice leaves the
// required-field flags to surface what is unknown. Any other failure
// rejects, keeping the device unloaded instead of blank-hydrated.
const loadData = async (device: BaseDevice): Promise<Record<string, unknown>> => {
    try {
        const response = await devices.getVxlan({ name: device.id.name });
        return {
            vni: response.vni,
            dstPort: response.dst_port,
            srcMac: response.src_mac,
            dstMac: response.dst_mac,
            srcIp: response.src_ip,
            dstIp: response.dst_ip,
        };
    } catch (error) {
        if (error instanceof ApiError && error.status === 404) {
            return {};
        }
        throw error;
    }
};

export const deviceType: DeviceTypeManifest = {
    type: 'vxlan',
    label: 'VXLAN',
    pluralLabel: 'VXLAN',
    navOrder: 40,
    icon: IconVxlan,
    accentColor: 'var(--blue)',
    kindTag: (device) => {
        const { vni } = vxlanExt(device);
        return `VXLAN · ${vni ?? '—'}`;
    },
    typeDescription: 'tunnel (vxlan)',
    rowSubtitle: () => 'vxlan · —',
    rowBadge: (device) => {
        const { vni } = vxlanExt(device);
        return vni !== undefined ? String(vni) : undefined;
    },
    parentGroupLabel: '∅ orphan VXLANs',
    propertyRows: (device) => {
        const { vni, dstPort, srcMac, dstMac, srcIp, dstIp } = vxlanExt(device);
        return [
            { label: 'VNI', value: vni !== undefined ? String(vni) : '—', mono: true },
            { label: 'Dst port', value: dstPort !== undefined ? String(dstPort) : '—', mono: true },
            { label: 'Src MAC', value: srcMac || '—', mono: true },
            { label: 'Dst MAC', value: dstMac || '—', mono: true },
            { label: 'Src IP', value: srcIp || '—', mono: true },
            { label: 'Dst IP', value: dstIp || '—', mono: true },
        ];
    },
    createDefaults: () => ({
        vni: 0,
        dstPort: DEFAULT_DST_PORT,
        srcMac: '',
        dstMac: '',
        srcIp: '',
        dstIp: '',
    }),
    loadData,
    extValid: (device) => isValidExt(vxlanExt(device)),
    extDirty: (device, snapshot) => {
        const current = vxlanExt(device);
        const clean = snapshot ? vxlanExt(snapshot) : undefined;
        return device.isNew || (clean != null && (
            current.vni !== clean.vni
            || current.dstPort !== clean.dstPort
            || current.srcMac !== clean.srcMac
            || current.dstMac !== clean.dstMac
            || current.srcIp !== clean.srcIp
            || current.dstIp !== clean.dstIp
        ));
    },
    diffYaml: (device) => {
        const ext = resolvedExt(device);
        return {
            vni: ext.vni,
            dst_port: ext.dstPort,
            src_mac: ext.srcMac,
            dst_mac: ext.dstMac,
            src_ip: ext.srcIp,
            dst_ip: ext.dstIp,
        };
    },
    loadDetail: () => import('./VxlanDetail'),
    save,
};
