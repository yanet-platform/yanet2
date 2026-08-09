import React, { useCallback } from 'react';
import { Label, Select, Text, TextInput } from '@gravity-ui/uikit';
import {
    parseDurationToNs,
    isValidNonzeroIPv6Address,
    isValidNonzeroMAC,
    type SyncConfigFormFields,
} from '@yanet/core/utils';
import type { AclMapSyncDraft } from './draftReducer';

interface MapSyncPanelProps {
    /** Current map + sync draft for the active config. */
    value: AclMapSyncDraft;
    /** Available fwstate-map names, fetched from FWStateMapService.ListMaps. */
    maps: string[];
    /** True when the config has a CREATE_STATE rule: map + sync are required. */
    required: boolean;
    /** Patch the draft (merged into the active config's map+sync state). */
    onChange: (next: AclMapSyncDraft) => void;
}

/**
 * Config-level map selector + sync-config form for the ACL page.
 *
 * Mirrors the fwstate configuration tab's sync field set (same labels, inputs,
 * validation) reusing the shared fwstateSync form helpers. Shown by the ACL
 * page when the config has a CREATE_STATE rule (map + sync required) or when
 * a map is already selected.
 */
const MapSyncPanel: React.FC<MapSyncPanelProps> = ({ value, maps, required, onChange }) => {
    const { mapName, sync } = value;

    const patchSync = useCallback((patch: Partial<SyncConfigFormFields>): void => {
        onChange({ ...value, sync: { ...sync, ...patch } });
    }, [onChange, sync, value]);

    const useMulticast = sync.syncMode === 'multicast' || sync.syncMode === 'both';
    const useUnicast = sync.syncMode === 'unicast' || sync.syncMode === 'both';
    const multicastAddrError = !useMulticast ? undefined : !isValidNonzeroIPv6Address(sync.dstAddrMulticast) ? 'Non-zero IPv6 required' : undefined;
    const multicastPortError = !useMulticast ? undefined : sync.portMulticast === 0 ? 'Port required' : sync.portMulticast < 0 || sync.portMulticast > 65535 ? '0..65535' : undefined;
    const unicastAddrError = !useUnicast ? undefined : !isValidNonzeroIPv6Address(sync.dstAddrUnicast) ? 'Non-zero IPv6 required' : undefined;
    const unicastPortError = !useUnicast ? undefined : sync.portUnicast === 0 ? 'Port required' : sync.portUnicast < 0 || sync.portUnicast > 65535 ? '0..65535' : undefined;

    return (
        <section className="acl-map-sync-panel">
            <div className="acl-map-sync-panel__head">
                <Text variant="subheader-2">FWState map &amp; sync</Text>
                {required
                    ? <Label theme="warning" size="s">Required: a rule uses +state</Label>
                    : <Label theme="unknown" size="s">Optional</Label>}
            </div>
            <p className="acl-map-sync-panel__note">
                A config may borrow a standalone fwstate-map to emit CREATE_STATE sync packets.
                Required iff any rule uses the <code>+state</code> action; forbidden otherwise
                (the map and sync fields are dropped on save when no <code>+state</code> rule exists).
            </p>

            <div className="acl-map-sync-panel__field">
                <Text variant="caption-2" color="secondary">FWState map</Text>
                {maps.length === 0 ? (
                    <Text color="secondary">
                        No fwstate-maps exist yet. Create one under FWState ▸ Maps.
                    </Text>
                ) : (
                    <Select
                        size="m"
                        value={[mapName]}
                        options={maps.map((name) => ({ value: name, content: name }))}
                        onUpdate={(next) => onChange({ ...value, mapName: next[0] ?? '' })}
                        placeholder={required ? 'Select a named map' : 'None'}
                    />
                )}
            </div>
            {required && maps.length > 0 && !mapName && (
                <Text color="danger" className="acl-map-sync-panel__error">
                    Select a fwstate-map — a +state rule requires one.
                </Text>
            )}

            <div className="acl-map-sync-grid">
                <label className="acl-map-sync-field acl-map-sync-grid__src">
                    <Text variant="caption-2" color="secondary">Sync source address</Text>
                    <TextInput value={sync.srcAddr} onUpdate={(srcAddr) => patchSync({ srcAddr })} error={!isValidNonzeroIPv6Address(sync.srcAddr) ? 'Non-zero IPv6 required' : undefined} placeholder="2001:db8::1" />
                </label>
                <label className="acl-map-sync-field acl-map-sync-grid__mac">
                    <Text variant="caption-2" color="secondary">Destination MAC</Text>
                    <TextInput value={sync.dstEther} onUpdate={(dstEther) => patchSync({ dstEther })} error={!isValidNonzeroMAC(sync.dstEther) ? 'Non-zero MAC required' : undefined} placeholder="aa:bb:cc:dd:ee:ff" />
                </label>
                <label className="acl-map-sync-field acl-map-sync-grid__mode">
                    <Text variant="caption-2" color="secondary">Endpoint mode</Text>
                    <Select
                        value={[sync.syncMode]}
                        options={[
                            { value: 'multicast', content: 'Multicast' },
                            { value: 'unicast', content: 'Unicast' },
                            { value: 'both', content: 'Both' },
                        ]}
                        onUpdate={(next) => patchSync({ syncMode: (next[0] as SyncConfigFormFields['syncMode']) || 'multicast' })}
                    />
                </label>
                {useMulticast && (
                    <div className="acl-map-sync-grid__endpoint">
                        <div className="acl-map-sync-field">
                            <Text variant="caption-2" color="secondary">Multicast endpoint</Text>
                            <div className="acl-map-sync-endpoint-row">
                                <label className="acl-map-sync-field">
                                    <Text variant="caption-2" color="secondary">Address</Text>
                                    <TextInput value={sync.dstAddrMulticast} onUpdate={(dstAddrMulticast) => patchSync({ dstAddrMulticast })} error={multicastAddrError} placeholder="ff02::1" />
                                </label>
                                <label className="acl-map-sync-field">
                                    <Text variant="caption-2" color="secondary">Port</Text>
                                    <TextInput type="number" value={String(sync.portMulticast)} onUpdate={(next) => patchSync({ portMulticast: Number(next) })} error={multicastPortError} placeholder="2000" />
                                </label>
                            </div>
                        </div>
                    </div>
                )}
                {useUnicast && (
                    <div className="acl-map-sync-grid__endpoint">
                        <div className="acl-map-sync-field">
                            <Text variant="caption-2" color="secondary">Unicast endpoint</Text>
                            <div className="acl-map-sync-endpoint-row">
                                <label className="acl-map-sync-field">
                                    <Text variant="caption-2" color="secondary">Address</Text>
                                    <TextInput value={sync.dstAddrUnicast} onUpdate={(dstAddrUnicast) => patchSync({ dstAddrUnicast })} error={unicastAddrError} placeholder="2001:db8::2" />
                                </label>
                                <label className="acl-map-sync-field">
                                    <Text variant="caption-2" color="secondary">Port</Text>
                                    <TextInput type="number" value={String(sync.portUnicast)} onUpdate={(next) => patchSync({ portUnicast: Number(next) })} error={unicastPortError} placeholder="2000" />
                                </label>
                            </div>
                        </div>
                    </div>
                )}
            </div>

            <div className="acl-map-sync-timeouts">
                <label className="acl-map-sync-field">
                    <Text variant="caption-2" color="secondary">TCP SYN+ACK</Text>
                    <TextInput type="number" value={sync.tcpSynAck} onUpdate={(tcpSynAck) => patchSync({ tcpSynAck })} error={parseDurationToNs(sync.tcpSynAck) ? undefined : 'Enter seconds'} endContent={<Text className="acl-map-sync-unit" variant="caption-2" color="secondary">s</Text>} />
                </label>
                <label className="acl-map-sync-field">
                    <Text variant="caption-2" color="secondary">TCP SYN</Text>
                    <TextInput type="number" value={sync.tcpSyn} onUpdate={(tcpSyn) => patchSync({ tcpSyn })} error={parseDurationToNs(sync.tcpSyn) ? undefined : 'Enter seconds'} endContent={<Text className="acl-map-sync-unit" variant="caption-2" color="secondary">s</Text>} />
                </label>
                <label className="acl-map-sync-field">
                    <Text variant="caption-2" color="secondary">TCP FIN</Text>
                    <TextInput type="number" value={sync.tcpFin} onUpdate={(tcpFin) => patchSync({ tcpFin })} error={parseDurationToNs(sync.tcpFin) ? undefined : 'Enter seconds'} endContent={<Text className="acl-map-sync-unit" variant="caption-2" color="secondary">s</Text>} />
                </label>
                <label className="acl-map-sync-field">
                    <Text variant="caption-2" color="secondary">TCP established</Text>
                    <TextInput type="number" value={sync.tcp} onUpdate={(tcp) => patchSync({ tcp })} error={parseDurationToNs(sync.tcp) ? undefined : 'Enter seconds'} endContent={<Text className="acl-map-sync-unit" variant="caption-2" color="secondary">s</Text>} />
                </label>
                <label className="acl-map-sync-field">
                    <Text variant="caption-2" color="secondary">UDP</Text>
                    <TextInput type="number" value={sync.udp} onUpdate={(udp) => patchSync({ udp })} error={parseDurationToNs(sync.udp) ? undefined : 'Enter seconds'} endContent={<Text className="acl-map-sync-unit" variant="caption-2" color="secondary">s</Text>} />
                </label>
                <label className="acl-map-sync-field">
                    <Text variant="caption-2" color="secondary">Default</Text>
                    <TextInput type="number" value={sync.defaultTimeout} onUpdate={(defaultTimeout) => patchSync({ defaultTimeout })} error={parseDurationToNs(sync.defaultTimeout) ? undefined : 'Enter seconds'} endContent={<Text className="acl-map-sync-unit" variant="caption-2" color="secondary">s</Text>} />
                </label>
            </div>
        </section>
    );
};

export default MapSyncPanel;
