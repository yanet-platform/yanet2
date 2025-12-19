import React, { useCallback } from 'react';
import { Box, Text, TextInput, Button, Card } from '@gravity-ui/uikit';
import type { FWStateFormProps } from './types';
import type { SyncConfig } from '../../api/acl';

// Decode base64 to bytes array
const base64ToBytes = (base64: string): number[] => {
    try {
        const binary = atob(base64);
        const bytes: number[] = [];
        for (let i = 0; i < binary.length; i++) {
            bytes.push(binary.charCodeAt(i));
        }
        return bytes;
    } catch {
        return [];
    }
};

// Get bytes from various input types
const getBytes = (input: string | Uint8Array | number[] | undefined): number[] => {
    if (!input) return [];
    if (typeof input === 'string') {
        return base64ToBytes(input);
    }
    return Array.from(input);
};

// Format bytes array to IPv6 string
const formatIPv6 = (input: string | Uint8Array | number[] | undefined): string => {
    const bytes = getBytes(input);
    if (bytes.length !== 16) return '';
    const parts: string[] = [];
    for (let i = 0; i < 16; i += 2) {
        const val = (bytes[i] << 8) | bytes[i + 1];
        parts.push(val.toString(16));
    }
    return parts.join(':');
};

// Format bytes array to MAC address string
const formatMAC = (input: string | Uint8Array | number[] | undefined): string => {
    const bytes = getBytes(input);
    if (bytes.length !== 6) return '';
    return bytes.map((b) => b.toString(16).padStart(2, '0')).join(':');
};

// Format nanoseconds to human-readable duration
const formatDuration = (nanos: number | string | undefined): string => {
    if (nanos === undefined || nanos === null) return '';
    const ns = typeof nanos === 'string' ? parseInt(nanos, 10) : nanos;
    if (isNaN(ns) || ns === 0) return '';

    const seconds = ns / 1_000_000_000;
    if (seconds >= 3600) {
        return `${Math.floor(seconds / 3600)}h`;
    }
    if (seconds >= 60) {
        return `${Math.floor(seconds / 60)}m`;
    }
    return `${Math.floor(seconds)}s`;
};

// Parse duration string to nanoseconds
const parseDuration = (str: string): number | undefined => {
    const trimmed = str.trim();
    if (!trimmed) return undefined;

    const match = trimmed.match(/^(\d+(?:\.\d+)?)\s*(s|m|h|ms|ns)?$/i);
    if (!match) return undefined;

    const value = parseFloat(match[1]);
    const unit = (match[2] || 's').toLowerCase();

    switch (unit) {
        case 'ns':
            return Math.floor(value);
        case 'ms':
            return Math.floor(value * 1_000_000);
        case 's':
            return Math.floor(value * 1_000_000_000);
        case 'm':
            return Math.floor(value * 60 * 1_000_000_000);
        case 'h':
            return Math.floor(value * 3600 * 1_000_000_000);
        default:
            return undefined;
    }
};

// Parse IPv6 string to bytes array
const parseIPv6 = (str: string): number[] | undefined => {
    const trimmed = str.trim();
    if (!trimmed) return undefined;

    // Handle :: expansion
    let fullAddr = trimmed;
    if (trimmed.includes('::')) {
        const parts = trimmed.split('::');
        const left = parts[0] ? parts[0].split(':') : [];
        const right = parts[1] ? parts[1].split(':') : [];
        const missing = 8 - left.length - right.length;
        const middle = Array(missing).fill('0');
        fullAddr = [...left, ...middle, ...right].join(':');
    }

    const parts = fullAddr.split(':');
    if (parts.length !== 8) return undefined;

    const bytes: number[] = [];
    for (const part of parts) {
        const num = parseInt(part || '0', 16);
        if (isNaN(num) || num < 0 || num > 0xffff) return undefined;
        bytes.push((num >> 8) & 0xff);
        bytes.push(num & 0xff);
    }
    return bytes;
};

// Parse MAC address string to bytes array
const parseMAC = (str: string): number[] | undefined => {
    const trimmed = str.trim();
    if (!trimmed) return undefined;

    const parts = trimmed.split(/[:-]/);
    if (parts.length !== 6) return undefined;

    const bytes: number[] = [];
    for (const part of parts) {
        const num = parseInt(part, 16);
        if (isNaN(num) || num < 0 || num > 255) return undefined;
        bytes.push(num);
    }
    return bytes;
};

interface FormFieldProps {
    label: string;
    value: string;
    onChange: (value: string) => void;
    placeholder?: string;
    hint?: string;
}

const FormField: React.FC<FormFieldProps> = ({ label, value, onChange, placeholder, hint }) => (
    <Box style={{ marginBottom: 16 }}>
        <Text variant="body-2" color="secondary" style={{ display: 'block', marginBottom: 4 }}>
            {label}
        </Text>
        <TextInput
            value={value}
            onUpdate={onChange}
            placeholder={placeholder}
            style={{ width: '100%' }}
        />
        {hint && (
            <Text variant="caption-2" color="secondary" style={{ display: 'block', marginTop: 2 }}>
                {hint}
            </Text>
        )}
    </Box>
);

export const FWStateForm: React.FC<FWStateFormProps> = ({
    mapConfig,
    syncConfig,
    onMapConfigChange,
    onSyncConfigChange,
    onSave,
    hasChanges,
}) => {
    // Map config handlers
    const handleIndexSizeChange = useCallback((value: string) => {
        const num = parseInt(value, 10);
        onMapConfigChange({
            ...mapConfig,
            indexSize: isNaN(num) ? undefined : num,
        });
    }, [mapConfig, onMapConfigChange]);

    const handleExtraBucketCountChange = useCallback((value: string) => {
        const num = parseInt(value, 10);
        onMapConfigChange({
            ...mapConfig,
            extraBucketCount: isNaN(num) ? undefined : num,
        });
    }, [mapConfig, onMapConfigChange]);

    // Sync config handlers
    const createSyncConfigHandler = useCallback(
        <K extends keyof SyncConfig>(key: K, parser?: (str: string) => SyncConfig[K]) =>
            (value: string) => {
                const parsedValue = parser ? parser(value) : (value as SyncConfig[K]);
                onSyncConfigChange({
                    ...syncConfig,
                    [key]: parsedValue,
                });
            },
        [syncConfig, onSyncConfigChange]
    );

    return (
        <Box style={{ padding: '16px 0', overflowY: 'auto', flex: 1 }}>
            {/* Map Config Section */}
            <Card style={{ marginBottom: 24, padding: 20 }}>
                <Text variant="subheader-2" style={{ display: 'block', marginBottom: 16 }}>
                    Map Configuration
                </Text>

                <Box style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
                    <FormField
                        label="Index Size"
                        value={mapConfig?.indexSize?.toString() || ''}
                        onChange={handleIndexSizeChange}
                        placeholder="e.g., 65536"
                        hint="Size of the hash table index"
                    />
                    <FormField
                        label="Extra Bucket Count"
                        value={mapConfig?.extraBucketCount?.toString() || ''}
                        onChange={handleExtraBucketCountChange}
                        placeholder="e.g., 1024"
                        hint="Number of extra buckets for collisions"
                    />
                </Box>
            </Card>

            {/* Sync Config Section */}
            <Card style={{ marginBottom: 24, padding: 20 }}>
                <Text variant="subheader-2" style={{ display: 'block', marginBottom: 16 }}>
                    Sync Configuration
                </Text>

                <Box style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
                    <FormField
                        label="Source IPv6 Address"
                        value={formatIPv6(syncConfig?.srcAddr)}
                        onChange={createSyncConfigHandler('srcAddr', parseIPv6)}
                        placeholder="e.g., 2001:db8::1"
                    />
                    <FormField
                        label="Destination MAC Address"
                        value={formatMAC(syncConfig?.dstEther)}
                        onChange={createSyncConfigHandler('dstEther', parseMAC)}
                        placeholder="e.g., 00:11:22:33:44:55"
                    />
                    <FormField
                        label="Multicast IPv6 Address"
                        value={formatIPv6(syncConfig?.dstAddrMulticast)}
                        onChange={createSyncConfigHandler('dstAddrMulticast', parseIPv6)}
                        placeholder="e.g., ff02::1"
                    />
                    <FormField
                        label="Multicast Port"
                        value={syncConfig?.portMulticast?.toString() || ''}
                        onChange={(v) => {
                            const num = parseInt(v, 10);
                            onSyncConfigChange({
                                ...syncConfig,
                                portMulticast: isNaN(num) ? undefined : num,
                            });
                        }}
                        placeholder="e.g., 5000"
                    />
                    <FormField
                        label="Unicast IPv6 Address"
                        value={formatIPv6(syncConfig?.dstAddrUnicast)}
                        onChange={createSyncConfigHandler('dstAddrUnicast', parseIPv6)}
                        placeholder="e.g., 2001:db8::2"
                    />
                    <FormField
                        label="Unicast Port"
                        value={syncConfig?.portUnicast?.toString() || ''}
                        onChange={(v) => {
                            const num = parseInt(v, 10);
                            onSyncConfigChange({
                                ...syncConfig,
                                portUnicast: isNaN(num) ? undefined : num,
                            });
                        }}
                        placeholder="e.g., 5001"
                    />
                </Box>
            </Card>

            {/* Timeout Config Section */}
            <Card style={{ marginBottom: 24, padding: 20 }}>
                <Text variant="subheader-2" style={{ display: 'block', marginBottom: 16 }}>
                    Connection Timeouts
                </Text>

                <Box style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 16 }}>
                    <FormField
                        label="TCP SYN-ACK"
                        value={formatDuration(syncConfig?.tcpSynAck)}
                        onChange={(v) => {
                            onSyncConfigChange({
                                ...syncConfig,
                                tcpSynAck: parseDuration(v),
                            });
                        }}
                        placeholder="e.g., 60s"
                        hint="TCP SYN-ACK timeout"
                    />
                    <FormField
                        label="TCP SYN"
                        value={formatDuration(syncConfig?.tcpSyn)}
                        onChange={(v) => {
                            onSyncConfigChange({
                                ...syncConfig,
                                tcpSyn: parseDuration(v),
                            });
                        }}
                        placeholder="e.g., 30s"
                        hint="TCP SYN timeout"
                    />
                    <FormField
                        label="TCP FIN"
                        value={formatDuration(syncConfig?.tcpFin)}
                        onChange={(v) => {
                            onSyncConfigChange({
                                ...syncConfig,
                                tcpFin: parseDuration(v),
                            });
                        }}
                        placeholder="e.g., 30s"
                        hint="TCP FIN timeout"
                    />
                    <FormField
                        label="TCP Established"
                        value={formatDuration(syncConfig?.tcp)}
                        onChange={(v) => {
                            onSyncConfigChange({
                                ...syncConfig,
                                tcp: parseDuration(v),
                            });
                        }}
                        placeholder="e.g., 1h"
                        hint="TCP established timeout"
                    />
                    <FormField
                        label="UDP"
                        value={formatDuration(syncConfig?.udp)}
                        onChange={(v) => {
                            onSyncConfigChange({
                                ...syncConfig,
                                udp: parseDuration(v),
                            });
                        }}
                        placeholder="e.g., 30s"
                        hint="UDP timeout"
                    />
                    <FormField
                        label="Default"
                        value={formatDuration(syncConfig?.default)}
                        onChange={(v) => {
                            onSyncConfigChange({
                                ...syncConfig,
                                default: parseDuration(v),
                            });
                        }}
                        placeholder="e.g., 60s"
                        hint="Default timeout"
                    />
                </Box>
            </Card>

            <Box style={{ display: 'flex', justifyContent: 'flex-end' }}>
                <Button
                    view="action"
                    size="l"
                    onClick={onSave}
                    disabled={!hasChanges}
                >
                    Save FW State Settings
                </Button>
            </Box>
        </Box>
    );
};

