import type { BaseDevice } from '@yanet/core/registry';

/** Per-device editable tunnel state; Save replaces the whole set at once. */
export interface VxlanExt {
    vni?: number;
    dstPort?: number;
    srcMac?: string;
    dstMac?: string;
    srcIp?: string;
    dstIp?: string;
}

/** Read the vxlan ext slice from a BaseDevice, defaulting to empty. */
export const vxlanExt = (device: BaseDevice): VxlanExt =>
    (device.ext.vxlan as VxlanExt | undefined) ?? {};

// 4789 is the IANA-assigned VXLAN UDP destination port.
export const DEFAULT_DST_PORT = 4789;

/** Fill unset ext fields with the values Save would send on the wire. */
export const resolvedExt = (device: BaseDevice): Required<VxlanExt> => {
    const { vni, dstPort, srcMac, dstMac, srcIp, dstIp } = vxlanExt(device);
    return {
        vni: vni ?? 0,
        dstPort: dstPort ?? DEFAULT_DST_PORT,
        srcMac: srcMac ?? '',
        dstMac: dstMac ?? '',
        srcIp: srcIp ?? '',
        dstIp: dstIp ?? '',
    };
};
