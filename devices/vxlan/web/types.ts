import type { BaseDevice } from '@yanet/core/registry';

/** Per-device editable tunnel state; Save replaces the whole set at once.
 *
 * The numeric fields hold the number the server reported while the value
 * is clean, and the raw editor text while it is being edited — including
 * text that does not parse — so Save can be gated on exactly what the
 * editor shows instead of the last valid number.
 */
export interface VxlanExt {
    vni?: number | string;
    dstPort?: number | string;
    srcMac?: string;
    dstMac?: string;
    srcIp?: string;
    dstIp?: string;
}

/** The tunnel fields as Save submits them. */
export interface VxlanSettings {
    vni: number;
    dstPort: number;
    srcMac: string;
    dstMac: string;
    srcIp: string;
    dstIp: string;
}

/** Read the vxlan ext slice from a BaseDevice, defaulting to empty. */
export const vxlanExt = (device: BaseDevice): VxlanExt =>
    (device.ext.vxlan as VxlanExt | undefined) ?? {};

// 4789 is the IANA-assigned VXLAN UDP destination port.
export const DEFAULT_DST_PORT = 4789;

// VNI is a 24-bit field and the outer UDP port a 16-bit one; the control
// plane rejects anything outside these ranges. MACs are held to the
// colon-only form GetDevice serves back, which is stricter than the
// control plane's parser.
export const VNI_MAX = 0xFFFFFF;
export const DST_PORT_MIN = 1;
export const DST_PORT_MAX = 0xFFFF;
export const MAC_RE = /^(?:[0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$/;

const DIGITS_RE = /^\d+$/;

/** IPv4 dotted-quad check, rejecting the leading zeros netip refuses. */
export const isIPv4 = (value: string): boolean => {
    const parts = value.split('.');
    return parts.length === 4
        && parts.every((part) => /^(0|[1-9]\d{0,2})$/.test(part) && Number(part) <= 255);
};

/** Parse a whole-number field inside [min, max].
 *
 * An unset field resolves to `fallback` — the value Save would send for
 * it — while anything unparseable or out of range resolves to null.
 */
export const parseExtUInt = (
    raw: number | string | undefined,
    min: number,
    max: number,
    fallback: number,
): number | null => {
    if (raw === undefined) {
        return fallback;
    }
    const text = typeof raw === 'number' ? String(raw) : raw.trim();
    if (!DIGITS_RE.test(text)) {
        return null;
    }
    const parsed = Number(text);
    return parsed >= min && parsed <= max ? parsed : null;
};

/** Whether every tunnel field is in the shape Save submits.
 *
 * Mirrors the control plane's validation except the MAC colon-only form,
 * so a save can never be enabled for a request it would reject.
 */
export const isValidExt = (ext: VxlanExt): boolean =>
    parseExtUInt(ext.vni, 0, VNI_MAX, 0) !== null
    && parseExtUInt(ext.dstPort, DST_PORT_MIN, DST_PORT_MAX, DEFAULT_DST_PORT) !== null
    && (ext.srcMac !== undefined && MAC_RE.test(ext.srcMac))
    && (ext.dstMac !== undefined && MAC_RE.test(ext.dstMac))
    && (ext.srcIp !== undefined && isIPv4(ext.srcIp))
    && (ext.dstIp !== undefined && isIPv4(ext.dstIp));

/** Resolve the ext into the values Save sends on the wire.
 *
 * Undefined numeric fields fall back to their wire default. Call it only
 * on an ext that passed isValidExt: an invalid numeric edit resolves to
 * its fallback here, which is not what the editor shows.
 */
export const resolvedExt = (device: BaseDevice): VxlanSettings => {
    const { vni, dstPort, srcMac, dstMac, srcIp, dstIp } = vxlanExt(device);
    return {
        vni: parseExtUInt(vni, 0, VNI_MAX, 0) ?? 0,
        dstPort: parseExtUInt(dstPort, DST_PORT_MIN, DST_PORT_MAX, DEFAULT_DST_PORT)
            ?? DEFAULT_DST_PORT,
        srcMac: srcMac ?? '',
        dstMac: dstMac ?? '',
        srcIp: srcIp ?? '',
        dstIp: dstIp ?? '',
    };
};
