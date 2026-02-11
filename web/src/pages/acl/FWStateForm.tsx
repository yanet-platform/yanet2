import React, { useCallback } from 'react';
import { Box, Text, Button, Card } from '@gravity-ui/uikit';
import { InputFormField } from '../../components';
import { getBytes } from '../../utils/bytes';
import { formatIPv6FromBytes, parseIPv6ToBytes } from '../../utils/netip';
import { formatMACFromBytes, parseMACToBytes } from '../../utils/mac';
import type { FWStateFormProps } from './types';
import type { SyncConfig } from '../../api/acl';
import './FWStateForm.css';

// Format bytes array to IPv6 string (wrapper for base64/bytes input)
const formatIPv6 = (input: string | Uint8Array | number[] | undefined): string => {
    const bytes = getBytes(input);
    return formatIPv6FromBytes(bytes);
};

// Format bytes array to MAC address string (wrapper for base64/bytes input)
const formatMAC = (input: string | Uint8Array | number[] | undefined): string => {
    const bytes = getBytes(input);
    return formatMACFromBytes(bytes);
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
            index_size: isNaN(num) ? undefined : num,
        });
    }, [mapConfig, onMapConfigChange]);

    const handleExtraBucketCountChange = useCallback((value: string) => {
        const num = parseInt(value, 10);
        onMapConfigChange({
            ...mapConfig,
            extra_bucket_count: isNaN(num) ? undefined : num,
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
        <Box className="fwstate-form">
            {/* Map Config Section */}
            <Card className="fwstate-form__card">
                <Text variant="subheader-2" className="fwstate-form__section-title">
                    Map Configuration
                </Text>

                <Box className="fwstate-form__grid--2cols">
                    <InputFormField
                        label="Index Size"
                        value={mapConfig?.index_size?.toString() || ''}
                        onChange={handleIndexSizeChange}
                        placeholder="e.g., 65536"
                        hint="Size of the hash table index"
                    />
                    <InputFormField
                        label="Extra Bucket Count"
                        value={mapConfig?.extra_bucket_count?.toString() || ''}
                        onChange={handleExtraBucketCountChange}
                        placeholder="e.g., 1024"
                        hint="Number of extra buckets for collisions"
                    />
                </Box>
            </Card>

            {/* Sync Config Section */}
            <Card className="fwstate-form__card">
                <Text variant="subheader-2" className="fwstate-form__section-title">
                    Sync Configuration
                </Text>

                <Box className="fwstate-form__grid--2cols">
                    <InputFormField
                        label="Source IPv6 Address"
                        value={formatIPv6(syncConfig?.src_addr)}
                        onChange={createSyncConfigHandler('src_addr', parseIPv6ToBytes)}
                        placeholder="e.g., 2001:db8::1"
                    />
                    <InputFormField
                        label="Destination MAC Address"
                        value={formatMAC(syncConfig?.dst_ether)}
                        onChange={createSyncConfigHandler('dst_ether', parseMACToBytes)}
                        placeholder="e.g., 00:11:22:33:44:55"
                    />
                    <InputFormField
                        label="Multicast IPv6 Address"
                        value={formatIPv6(syncConfig?.dst_addr_multicast)}
                        onChange={createSyncConfigHandler('dst_addr_multicast', parseIPv6ToBytes)}
                        placeholder="e.g., ff02::1"
                    />
                    <InputFormField
                        label="Multicast Port"
                        value={syncConfig?.port_multicast?.toString() || ''}
                        onChange={(v) => {
                            const num = parseInt(v, 10);
                            onSyncConfigChange({
                                ...syncConfig,
                                port_multicast: isNaN(num) ? undefined : num,
                            });
                        }}
                        placeholder="e.g., 5000"
                    />
                    <InputFormField
                        label="Unicast IPv6 Address"
                        value={formatIPv6(syncConfig?.dst_addr_unicast)}
                        onChange={createSyncConfigHandler('dst_addr_unicast', parseIPv6ToBytes)}
                        placeholder="e.g., 2001:db8::2"
                    />
                    <InputFormField
                        label="Unicast Port"
                        value={syncConfig?.port_unicast?.toString() || ''}
                        onChange={(v) => {
                            const num = parseInt(v, 10);
                            onSyncConfigChange({
                                ...syncConfig,
                                port_unicast: isNaN(num) ? undefined : num,
                            });
                        }}
                        placeholder="e.g., 5001"
                    />
                </Box>
            </Card>

            {/* Timeout Config Section */}
            <Card className="fwstate-form__card">
                <Text variant="subheader-2" className="fwstate-form__section-title">
                    Connection Timeouts
                </Text>

                <Box className="fwstate-form__grid--3cols">
                    <InputFormField
                        label="TCP SYN-ACK"
                        value={formatDuration(syncConfig?.tcp_syn_ack)}
                        onChange={(v) => {
                            onSyncConfigChange({
                                ...syncConfig,
                                tcp_syn_ack: parseDuration(v),
                            });
                        }}
                        placeholder="e.g., 60s"
                        hint="TCP SYN-ACK timeout"
                    />
                    <InputFormField
                        label="TCP SYN"
                        value={formatDuration(syncConfig?.tcp_syn)}
                        onChange={(v) => {
                            onSyncConfigChange({
                                ...syncConfig,
                                tcp_syn: parseDuration(v),
                            });
                        }}
                        placeholder="e.g., 30s"
                        hint="TCP SYN timeout"
                    />
                    <InputFormField
                        label="TCP FIN"
                        value={formatDuration(syncConfig?.tcp_fin)}
                        onChange={(v) => {
                            onSyncConfigChange({
                                ...syncConfig,
                                tcp_fin: parseDuration(v),
                            });
                        }}
                        placeholder="e.g., 30s"
                        hint="TCP FIN timeout"
                    />
                    <InputFormField
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
                    <InputFormField
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
                    <InputFormField
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

            <Box className="fwstate-form__actions">
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
