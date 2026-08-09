import { isValidIPAddress, parseIPToBytes, stringToIPAddress, ipAddressToString, type IPAddressWire } from './netip';
import { parseMACToBytes } from './mac';

// Wire shape of the fwstate/acl SyncConfig message. Both modules declare a
// proto SyncConfig with identical field numbers and types; this interface
// captures that shared shape so the form helpers below are reusable.
export interface SyncConfigWire {
    src_addr?: IPAddressWire;
    dst_ether?: { addr: string };
    dst_addr_multicast?: IPAddressWire;
    port_multicast?: number;
    dst_addr_unicast?: IPAddressWire;
    port_unicast?: number;
    tcp_syn_ack?: number;
    tcp_syn?: number;
    tcp_fin?: number;
    tcp?: number;
    udp?: number;
    default?: number;
}

// Form-level string fields mirroring the fwstate configuration tab. Timeouts
// are edited as seconds-strings and converted to/from nanoseconds on the wire.
export interface SyncConfigFormFields {
    srcAddr: string;
    dstEther: string;
    dstAddrMulticast: string;
    portMulticast: number;
    dstAddrUnicast: string;
    portUnicast: number;
    syncMode: 'multicast' | 'unicast' | 'both';
    tcpSynAck: string;
    tcpSyn: string;
    tcpFin: string;
    tcp: string;
    udp: string;
    defaultTimeout: string;
}

// Default connection timeouts in nanoseconds, matching the fwstate defaults.
export const DEFAULT_SYNC_TIMEOUTS = {
    tcpSynAck: 120_000_000_000,
    tcpSyn: 120_000_000_000,
    tcpFin: 120_000_000_000,
    tcp: 120_000_000_000,
    udp: 30_000_000_000,
    defaultTimeout: 16_000_000_000,
} satisfies Record<keyof Pick<SyncConfigFormFields, 'tcpSynAck' | 'tcpSyn' | 'tcpFin' | 'tcp' | 'udp' | 'defaultTimeout'>, number>;

export const zeroIPv6AddressWire = (): IPAddressWire => ({ addr: '::' });

export const formatDurationNsAsSeconds = (value: number): string => {
    if (!Number.isFinite(value) || value <= 0) return '';
    const seconds = value / 1_000_000_000;
    if (Number.isInteger(seconds)) return String(seconds);
    return seconds.toFixed(9).replace(/\.?0+$/, '');
};

export const parseDurationToNs = (value: string): number | null => {
    const trimmed = value.trim().toLowerCase();
    if (!trimmed) return null;
    const numberOnly = trimmed.match(/^\d+(?:\.\d+)?$/);
    if (numberOnly) {
        const seconds = Number(trimmed);
        if (!Number.isFinite(seconds) || seconds <= 0) return null;
        return Math.round(seconds * 1_000_000_000);
    }
    const unitMatch = trimmed.match(/^(\d+(?:\.\d+)?)(ns|ms|s|m|h)$/);
    if (!unitMatch) return null;
    const amount = Number(unitMatch[1]);
    if (!Number.isFinite(amount) || amount <= 0) return null;
    const unit = unitMatch[2];
    if (unit === 'ns') return Math.round(amount);
    if (unit === 'ms') return Math.round(amount * 1_000_000);
    if (unit === 's') return Math.round(amount * 1_000_000_000);
    if (unit === 'm') return Math.round(amount * 60 * 1_000_000_000);
    return Math.round(amount * 3600 * 1_000_000_000);
};

// NOTE: not exported — netip already exports an isValidIPv6Address with
// different semantics. Kept module-private as a helper for the nonzero check.
const isValidIPv6Address = (value: string): boolean => {
    return isValidIPAddress(value) && value.includes(':');
};

const isZeroIPv6Address = (value: string): boolean => {
    const bytes = parseIPToBytes(value);
    return Boolean(bytes && bytes.length === 16 && bytes.every((byte) => byte === 0));
};

export const isValidNonzeroIPv6Address = (value: string): boolean => {
    return isValidIPv6Address(value) && !isZeroIPv6Address(value);
};

export const isValidNonzeroMAC = (value: string): boolean => {
    const parsed = parseMACToBytes(value);
    return Boolean(parsed && parsed.some((byte) => byte !== 0));
};

// Convert a wire SyncConfig into form-level string fields. Missing/empty sync
// configs yield the default form so the UI always has editable placeholders.
export const syncConfigToFormFields = (sync: SyncConfigWire | undefined): SyncConfigFormFields => {
    const multicastAddress = ipAddressToString(sync?.dst_addr_multicast as IPAddressWire | undefined).trim();
    const unicastAddress = ipAddressToString(sync?.dst_addr_unicast as IPAddressWire | undefined).trim();
    const multicastPresent = isValidNonzeroIPv6Address(multicastAddress) && (sync?.port_multicast ?? 0) !== 0;
    const unicastPresent = isValidNonzeroIPv6Address(unicastAddress) && (sync?.port_unicast ?? 0) !== 0;
    const syncMode: SyncConfigFormFields['syncMode'] = multicastPresent && unicastPresent
        ? 'both'
        : unicastPresent
            ? 'unicast'
            : 'multicast';
    return {
        srcAddr: ipAddressToString(sync?.src_addr as IPAddressWire | undefined),
        dstEther: sync?.dst_ether?.addr ?? '',
        dstAddrMulticast: ipAddressToString(sync?.dst_addr_multicast as IPAddressWire | undefined),
        portMulticast: sync?.port_multicast ?? 0,
        dstAddrUnicast: ipAddressToString(sync?.dst_addr_unicast as IPAddressWire | undefined),
        portUnicast: sync?.port_unicast ?? 0,
        syncMode,
        tcpSynAck: formatDurationNsAsSeconds(sync?.tcp_syn_ack ?? DEFAULT_SYNC_TIMEOUTS.tcpSynAck),
        tcpSyn: formatDurationNsAsSeconds(sync?.tcp_syn ?? DEFAULT_SYNC_TIMEOUTS.tcpSyn),
        tcpFin: formatDurationNsAsSeconds(sync?.tcp_fin ?? DEFAULT_SYNC_TIMEOUTS.tcpFin),
        tcp: formatDurationNsAsSeconds(sync?.tcp ?? DEFAULT_SYNC_TIMEOUTS.tcp),
        udp: formatDurationNsAsSeconds(sync?.udp ?? DEFAULT_SYNC_TIMEOUTS.udp),
        defaultTimeout: formatDurationNsAsSeconds(sync?.default ?? DEFAULT_SYNC_TIMEOUTS.defaultTimeout),
    };
};

// Convert form-level string fields back into a wire SyncConfig. Unused
// endpoints (per syncMode) are zeroed so the server sees an explicit absence.
export const formFieldsToSyncConfig = (form: SyncConfigFormFields): SyncConfigWire => {
    const useMulticast = form.syncMode === 'multicast' || form.syncMode === 'both';
    const useUnicast = form.syncMode === 'unicast' || form.syncMode === 'both';
    return {
        src_addr: stringToIPAddress(form.srcAddr),
        dst_ether: { addr: form.dstEther },
        dst_addr_multicast: useMulticast ? stringToIPAddress(form.dstAddrMulticast) : zeroIPv6AddressWire(),
        port_multicast: useMulticast ? form.portMulticast : 0,
        dst_addr_unicast: useUnicast ? stringToIPAddress(form.dstAddrUnicast) : zeroIPv6AddressWire(),
        port_unicast: useUnicast ? form.portUnicast : 0,
        tcp_syn_ack: parseDurationToNs(form.tcpSynAck) ?? undefined,
        tcp_syn: parseDurationToNs(form.tcpSyn) ?? undefined,
        tcp_fin: parseDurationToNs(form.tcpFin) ?? undefined,
        tcp: parseDurationToNs(form.tcp) ?? undefined,
        udp: parseDurationToNs(form.udp) ?? undefined,
        default: parseDurationToNs(form.defaultTimeout) ?? undefined,
    };
};

// Report whether every form field parses to a valid value. Mirrors the
// validation the fwstate configuration tab applies before save.
export const validateSyncConfigFormFields = (form: SyncConfigFormFields): boolean => {
    const durationFields = [form.tcpSynAck, form.tcpSyn, form.tcpFin, form.tcp, form.udp, form.defaultTimeout];
    const useMulticast = form.syncMode === 'multicast' || form.syncMode === 'both';
    const useUnicast = form.syncMode === 'unicast' || form.syncMode === 'both';
    if (useMulticast && (form.portMulticast < 0 || form.portMulticast > 65535)) return false;
    if (useUnicast && (form.portUnicast < 0 || form.portUnicast > 65535)) return false;
    if (!isValidNonzeroIPv6Address(form.srcAddr)) return false;
    if (useMulticast && (!isValidNonzeroIPv6Address(form.dstAddrMulticast) || form.portMulticast === 0)) return false;
    if (useUnicast && (!isValidNonzeroIPv6Address(form.dstAddrUnicast) || form.portUnicast === 0)) return false;
    if (!isValidNonzeroMAC(form.dstEther)) return false;
    if (durationFields.some((value) => parseDurationToNs(value) === null)) return false;
    return true;
};
