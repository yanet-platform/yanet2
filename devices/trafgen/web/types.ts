import type { PacketRow } from '@yanet/core/components/packet';
import type { BaseDevice } from '@yanet/core/registry';

export type { PacketRow };

/** Per-device editable state for trafgen generators. */
export interface TrafgenExt {
    ratePps: number;
    framePackets: PacketRow[];
    truncated: boolean;
    stagedPcapBytes: Uint8Array | null;
    stagedFramePackets?: PacketRow[];
}

/** Read the trafgen ext slice from a BaseDevice with safe defaults. */
export const trafgenExt = (device: BaseDevice): TrafgenExt => {
    const raw = device.ext.trafgen as TrafgenExt | undefined;
    return raw ?? {
        ratePps: 0,
        framePackets: [],
        truncated: false,
        stagedPcapBytes: null,
    };
};
