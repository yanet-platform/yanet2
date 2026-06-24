import type { PacketRow } from '@yanet/core/components/packet';
import type { BaseDevice } from '@yanet/core/registry';

export type { PacketRow };

/** Per-device editable state for trafgen generators.
 *
 * Rate and pcap are applied live through their own API calls, so the ext only
 * mirrors the last server-confirmed values — it carries no pending/staged edits.
 */
export interface TrafgenExt {
    ratePps: number;
    framePackets: PacketRow[];
    truncated: boolean;
}

/** Read the trafgen ext slice from a BaseDevice with safe defaults. */
export const trafgenExt = (device: BaseDevice): TrafgenExt => {
    const raw = device.ext.trafgen as TrafgenExt | undefined;
    return raw ?? {
        ratePps: 0,
        framePackets: [],
        truncated: false,
    };
};
