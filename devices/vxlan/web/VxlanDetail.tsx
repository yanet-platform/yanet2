import React from 'react';
import type { DeviceDetailProps } from '@yanet/core/registry';
import {
    resolvedExt,
    vxlanExt,
    parseExtUInt,
    isIPv4,
    MAC_RE,
    VNI_MAX,
    DST_PORT_MIN,
    DST_PORT_MAX,
    DEFAULT_DST_PORT,
} from './types';

const DIGITS_RE = /^\d+$/;

interface FieldCardProps {
    id: string;
    title: string;
    note: string;
    value: string;
    placeholder: string;
    invalid: boolean;
    flag: string | null;
    onChange: (value: string) => void;
}

const FieldCard = ({
    id,
    title,
    note,
    value,
    placeholder,
    invalid,
    flag,
    onChange,
}: FieldCardProps): React.JSX.Element => (
    <div className="dv-vxlan-card">
        <div className="dv-vxlan-card-hd">
            <label className="dv-vxlan-card-title" htmlFor={id}>{title}</label>
            <span
                className={'dv-vxlan-flag' + (invalid ? ' dv-vxlan-flag--err' : '')}
                style={{ visibility: flag !== null ? 'visible' : 'hidden' }}
            >
                {flag ?? ''}
            </span>
        </div>
        <input
            id={id}
            className={'dv-vxlan-input mono' + (invalid ? ' dv-vxlan-input--invalid' : '')}
            type="text"
            value={value}
            placeholder={placeholder}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => onChange(e.target.value)}
        />
        <div className="dv-vxlan-card-note">{note}</div>
    </div>
);

/** Editors for the six tunnel tunables; edits flow straight into the ext slice.
 *
 * Every field binds the raw editor text, so what the flags judge is
 * exactly what Save would submit, and the manifest's extValid keeps the
 * Save button disabled until every field parses.
 */
const VxlanDetail = ({ device, onUpdateExt }: DeviceDetailProps): React.JSX.Element => {
    const ext = vxlanExt(device);
    const resolved = resolvedExt(device);

    // Until loadDeviceExt replaces the whole ext slice, it still holds the
    // empty defaults; edits made against those would be discarded by the
    // hydration response, so the editors wait for it.
    if (!device.loaded) {
        return (
            <div className="dv-section">
                <div className="dv-section-hd"><span>Tunnel settings</span></div>
                <div className="dv-vxlan-note">Loading tunnel configuration…</div>
            </div>
        );
    }

    // Whole numbers inside the control plane's accepted range; the raw
    // text stays in the ext until it re-parses, so an invalid edit keeps
    // the field flagged and Save disabled instead of silently reverting.
    const numericInvalid = (raw: number | string | undefined, min: number, max: number): boolean =>
        raw !== undefined && parseExtUInt(raw, min, max, 0) === null;

    const numericFlag = (raw: number | string, min: number, max: number): string => {
        const text = typeof raw === 'number' ? String(raw) : raw.trim();
        return DIGITS_RE.test(text) ? `${min}…${max}` : 'whole number only';
    };

    const vniInvalid = numericInvalid(ext.vni, 0, VNI_MAX);
    const dstPortInvalid = numericInvalid(ext.dstPort, DST_PORT_MIN, DST_PORT_MAX);

    const macField = (raw: string | undefined): { value: string; invalid: boolean; flag: string | null } => ({
        value: raw ?? '',
        invalid: raw === undefined || raw === '' || !MAC_RE.test(raw),
        flag: raw === undefined || raw === '' ? 'required' : MAC_RE.test(raw) ? null : 'aa:bb:cc:dd:ee:ff',
    });
    const ipField = (raw: string | undefined): { value: string; invalid: boolean; flag: string | null } => ({
        value: raw ?? '',
        invalid: raw === undefined || raw === '' || !isIPv4(raw),
        flag: raw === undefined || raw === '' ? 'required' : isIPv4(raw) ? null : 'IPv4 address only',
    });

    const srcMac = macField(ext.srcMac);
    const dstMac = macField(ext.dstMac);
    const srcIp = ipField(ext.srcIp);
    const dstIp = ipField(ext.dstIp);

    return (
        <div className="dv-section">
            <div className="dv-section-hd"><span>Tunnel settings</span></div>
            <div className="dv-vxlan-cards">
                <FieldCard
                    id="dv-vxlan-vni"
                    title="VNI"
                    note="VXLAN network identifier, 24 bits."
                    value={ext.vni === undefined ? String(resolved.vni) : String(ext.vni)}
                    placeholder="0"
                    invalid={vniInvalid}
                    flag={vniInvalid && ext.vni !== undefined ? numericFlag(ext.vni, 0, VNI_MAX) : null}
                    onChange={(value) => onUpdateExt({ vni: value })}
                />
                <FieldCard
                    id="dv-vxlan-dst-port"
                    title="Dst port"
                    note={`Outer UDP destination port; ${DEFAULT_DST_PORT} is the IANA default.`}
                    value={ext.dstPort === undefined ? String(resolved.dstPort) : String(ext.dstPort)}
                    placeholder={String(DEFAULT_DST_PORT)}
                    invalid={dstPortInvalid}
                    flag={dstPortInvalid && ext.dstPort !== undefined
                        ? numericFlag(ext.dstPort, DST_PORT_MIN, DST_PORT_MAX)
                        : null}
                    onChange={(value) => onUpdateExt({ dstPort: value })}
                />
                <FieldCard
                    id="dv-vxlan-src-mac"
                    title="Src MAC"
                    note="Outer source MAC of encapsulated frames."
                    value={srcMac.value}
                    placeholder="aa:bb:cc:dd:ee:ff"
                    invalid={srcMac.invalid}
                    flag={srcMac.flag}
                    onChange={(value) => onUpdateExt({ srcMac: value })}
                />
                <FieldCard
                    id="dv-vxlan-dst-mac"
                    title="Dst MAC"
                    note="Outer destination MAC of encapsulated frames."
                    value={dstMac.value}
                    placeholder="aa:bb:cc:dd:ee:ff"
                    invalid={dstMac.invalid}
                    flag={dstMac.flag}
                    onChange={(value) => onUpdateExt({ dstMac: value })}
                />
                <FieldCard
                    id="dv-vxlan-src-ip"
                    title="Src IP"
                    note="Outer source IPv4 underlay address."
                    value={srcIp.value}
                    placeholder="10.0.0.1"
                    invalid={srcIp.invalid}
                    flag={srcIp.flag}
                    onChange={(value) => onUpdateExt({ srcIp: value })}
                />
                <FieldCard
                    id="dv-vxlan-dst-ip"
                    title="Dst IP"
                    note="Outer destination IPv4 underlay address."
                    value={dstIp.value}
                    placeholder="10.0.0.2"
                    invalid={dstIp.invalid}
                    flag={dstIp.flag}
                    onChange={(value) => onUpdateExt({ dstIp: value })}
                />
            </div>
            <div className="dv-vxlan-note">
                Update replaces the whole tunnel configuration in one call — the control
                plane requires every field, so fill them all before saving.
            </div>
        </div>
    );
};

export default VxlanDetail;
