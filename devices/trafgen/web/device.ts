import './trafgen.scss';
import { toDevicePayload } from '@yanet/core/api';
import { base64ToUint8Array, parsePacket } from '@yanet/core/utils/packetParser';
import type { PacketRow } from '@yanet/core/components/packet';
import type { BaseDevice, DeviceTypeManifest } from '@yanet/core/registry';
import { trafgen } from './api';
import { trafgenExt } from './types';
import { IconGenerator } from './icon';

/** Encode a Uint8Array as base64. */
const uint8ArrayToBase64 = (bytes: Uint8Array): string => {
    let binary = '';
    for (let idx = 0; idx < bytes.length; idx++) {
        binary += String.fromCharCode(bytes[idx]);
    }
    return btoa(binary);
};

/** Decode base64 L2 frames into displayable packet rows. */
const framesToPackets = (frames: string[] | undefined): PacketRow[] => {
    let idx = 0;
    return (frames ?? []).map(b64 => ({ id: idx++, parsed: parsePacket(base64ToUint8Array(b64)) }));
};

const loadData = async (device: BaseDevice): Promise<Record<string, unknown>> => {
    const name = device.id.name ?? '';
    let ratePps = 0;
    let framePackets: PacketRow[] = [];
    let truncated = false;
    try {
        const [cfg, pkts] = await Promise.all([
            trafgen.showConfig(name),
            trafgen.showPackets(name),
        ]);
        ratePps = Number(cfg.rate_pps ?? '0');
        framePackets = framesToPackets(pkts.packets);
        truncated = pkts.truncated ?? false;
    } catch {
        // No server-side config yet; fall through with the empty defaults.
    }
    return { ratePps, framePackets, truncated, stagedPcapBytes: null };
};

const save = async (
    device: BaseDevice,
    snapshot: BaseDevice | undefined,
): Promise<Record<string, unknown>> => {
    const name = device.id.name ?? '';
    const ext = trafgenExt(device);
    const snapExt = snapshot ? trafgenExt(snapshot) : undefined;

    const pipelinesDirty = device.isNew || !snapshot
        || JSON.stringify(device.inputPipelines) !== JSON.stringify(snapshot.inputPipelines)
        || JSON.stringify(device.outputPipelines) !== JSON.stringify(snapshot.outputPipelines);
    const rateDirty = device.isNew
        || (snapExt != null && ext.ratePps !== snapExt.ratePps);
    const pcapStaged = ext.stagedPcapBytes != null;

    if (pipelinesDirty) {
        await trafgen.updateDevice({
            name,
            device: toDevicePayload(device.inputPipelines, device.outputPipelines),
        });
    }
    if (rateDirty) {
        await trafgen.setRate({ name, rate_pps: ext.ratePps });
    }

    let framePackets = ext.framePackets;
    let truncated = ext.truncated;
    if (pcapStaged && ext.stagedPcapBytes) {
        await trafgen.uploadPcap({ name, pcap: uint8ArrayToBase64(ext.stagedPcapBytes) });
        const pkts = await trafgen.showPackets(name);
        framePackets = framesToPackets(pkts.packets);
        truncated = pkts.truncated ?? false;
    }

    return {
        ratePps: ext.ratePps,
        framePackets,
        truncated,
        stagedPcapBytes: null,
        stagedFramePackets: undefined,
    };
};

export const deviceType: DeviceTypeManifest = {
    type: 'trafgen',
    label: 'Generator',
    pluralLabel: 'Generators',
    navOrder: 30,
    icon: IconGenerator,
    accentColor: '#e8a13c',
    kindTag: () => 'GENERATOR',
    typeDescription: 'generator (trafgen)',
    trafficSource: true,
    rowSubtitle: () => 'generator · —',
    propertyRows: (device) => {
        const ext = trafgenExt(device);
        return [
            { label: 'Rate', value: `${ext.ratePps} pps`, mono: true },
            { label: 'Frames', value: String(ext.framePackets.length), mono: true },
        ];
    },
    createDefaults: () => ({
        ratePps: 0,
        framePackets: [],
        truncated: false,
        stagedPcapBytes: null,
    }),
    loadData,
    save,
    extDirty: (device, snapshot) => {
        const ext = trafgenExt(device);
        const snapExt = snapshot ? trafgenExt(snapshot) : undefined;
        return device.isNew
            || (snapExt != null && ext.ratePps !== snapExt.ratePps)
            || ext.stagedPcapBytes != null;
    },
    confirmViaDiff: false,
    loadDetail: () => import('./TrafgenDetail'),
};
