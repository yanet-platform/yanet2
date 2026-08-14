import React, { useState, useEffect } from 'react';
import type { DeviceDetailProps } from '@yanet/core/registry';
import { resolvedExt, DEFAULT_DST_PORT } from './types';

// VNI is a 24-bit field and the outer UDP port a 16-bit one; the control
// plane rejects anything outside these ranges, as it does MACs and IPs that
// fail to parse, so the flags below name what Save would reject. MACs are
// held to the colon-only form GetDevice serves back, which is stricter than
// the control plane's parser.
const VNI_MAX = 0xFFFFFF;
const DST_PORT_MIN = 1;
const DST_PORT_MAX = 0xFFFF;
const DIGITS_RE = /^\d+$/;
const MAC_RE = /^(?:[0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$/;

/** IPv4 dotted-quad check, rejecting the leading zeros netip refuses. */
const isIPv4 = (value: string): boolean => {
    const parts = value.split('.');
    return parts.length === 4
        && parts.every((part) => /^(0|[1-9]\d{0,2})$/.test(part) && Number(part) <= 255);
};

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
 * Text fields bind the ext directly. The two numeric fields keep a local
 * string so intermediate keystrokes stay editable, pushing a parsed value
 * into the ext only while it is valid — Save always sends a parseable one.
 */
const VxlanDetail = ({ device, onUpdateExt }: DeviceDetailProps): React.JSX.Element => {
    const resolved = resolvedExt(device);
    const deviceName = device.id.name ?? '';

    const [vniInput, setVniInput] = useState(String(resolved.vni));
    const [dstPortInput, setDstPortInput] = useState(String(resolved.dstPort));

    // Resync the numeric inputs when the device or its saved ext changes,
    // without clobbering a local spelling of the same value (e.g. "007").
    useEffect(() => {
        setVniInput((prev) =>
            DIGITS_RE.test(prev) && Number(prev) === resolved.vni ? prev : String(resolved.vni));
    }, [deviceName, resolved.vni]);
    useEffect(() => {
        setDstPortInput((prev) =>
            DIGITS_RE.test(prev) && Number(prev) === resolved.dstPort ? prev : String(resolved.dstPort));
    }, [deviceName, resolved.dstPort]);

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

    // Push only whole numbers inside the control plane's accepted range.
    const handleNumericChange = (
        setInput: (value: string) => void,
        min: number,
        max: number,
        committed: number,
        key: 'vni' | 'dstPort',
        raw: string,
    ): void => {
        setInput(raw);
        const trimmed = raw.trim();
        if (DIGITS_RE.test(trimmed)) {
            const parsed = Number(trimmed);
            if (parsed >= min && parsed <= max && parsed !== committed) {
                onUpdateExt({ [key]: parsed });
            }
        }
    };

    const vniDigits = DIGITS_RE.test(vniInput.trim());
    const vniValue = vniDigits ? Number(vniInput.trim()) : NaN;
    const vniInvalid = !vniDigits || vniValue > VNI_MAX;

    const dstPortDigits = DIGITS_RE.test(dstPortInput.trim());
    const dstPortValue = dstPortDigits ? Number(dstPortInput.trim()) : NaN;
    const dstPortInvalid = !dstPortDigits || dstPortValue < DST_PORT_MIN || dstPortValue > DST_PORT_MAX;

    return (
        <div className="dv-section">
            <div className="dv-section-hd"><span>Tunnel settings</span></div>
            <div className="dv-vxlan-cards">
                <FieldCard
                    id="dv-vxlan-vni"
                    title="VNI"
                    note="VXLAN network identifier, 24 bits."
                    value={vniInput}
                    placeholder="0"
                    invalid={vniInvalid}
                    flag={!vniDigits ? 'whole number only' : vniValue > VNI_MAX ? `0…${VNI_MAX}` : null}
                    onChange={(value) =>
                        handleNumericChange(setVniInput, 0, VNI_MAX, resolved.vni, 'vni', value)}
                />
                <FieldCard
                    id="dv-vxlan-dst-port"
                    title="Dst port"
                    note={`Outer UDP destination port; ${DEFAULT_DST_PORT} is the IANA default.`}
                    value={dstPortInput}
                    placeholder={String(DEFAULT_DST_PORT)}
                    invalid={dstPortInvalid}
                    flag={!dstPortDigits
                        ? 'whole number only'
                        : dstPortValue < DST_PORT_MIN || dstPortValue > DST_PORT_MAX
                            ? `${DST_PORT_MIN}…${DST_PORT_MAX}`
                            : null}
                    onChange={(value) =>
                        handleNumericChange(setDstPortInput, DST_PORT_MIN, DST_PORT_MAX, resolved.dstPort, 'dstPort', value)}
                />
                <FieldCard
                    id="dv-vxlan-src-mac"
                    title="Src MAC"
                    note="Outer source MAC of encapsulated frames."
                    value={resolved.srcMac}
                    placeholder="aa:bb:cc:dd:ee:ff"
                    invalid={resolved.srcMac === '' || !MAC_RE.test(resolved.srcMac)}
                    flag={resolved.srcMac === '' ? 'required' : MAC_RE.test(resolved.srcMac) ? null : 'aa:bb:cc:dd:ee:ff'}
                    onChange={(value) => onUpdateExt({ srcMac: value })}
                />
                <FieldCard
                    id="dv-vxlan-dst-mac"
                    title="Dst MAC"
                    note="Outer destination MAC of encapsulated frames."
                    value={resolved.dstMac}
                    placeholder="aa:bb:cc:dd:ee:ff"
                    invalid={resolved.dstMac === '' || !MAC_RE.test(resolved.dstMac)}
                    flag={resolved.dstMac === '' ? 'required' : MAC_RE.test(resolved.dstMac) ? null : 'aa:bb:cc:dd:ee:ff'}
                    onChange={(value) => onUpdateExt({ dstMac: value })}
                />
                <FieldCard
                    id="dv-vxlan-src-ip"
                    title="Src IP"
                    note="Outer source IPv4 underlay address."
                    value={resolved.srcIp}
                    placeholder="10.0.0.1"
                    invalid={resolved.srcIp === '' || !isIPv4(resolved.srcIp)}
                    flag={resolved.srcIp === '' ? 'required' : isIPv4(resolved.srcIp) ? null : 'IPv4 address only'}
                    onChange={(value) => onUpdateExt({ srcIp: value })}
                />
                <FieldCard
                    id="dv-vxlan-dst-ip"
                    title="Dst IP"
                    note="Outer destination IPv4 underlay address."
                    value={resolved.dstIp}
                    placeholder="10.0.0.2"
                    invalid={resolved.dstIp === '' || !isIPv4(resolved.dstIp)}
                    flag={resolved.dstIp === '' ? 'required' : isIPv4(resolved.dstIp) ? null : 'IPv4 address only'}
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
