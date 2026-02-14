import React, { useState, useCallback, useEffect } from 'react';
import { Box, Dialog, TextInput } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import { useDialogKeyboardShortcut } from '../../hooks';
import { formatMACAddress, macToUint64 } from '../../utils/mac';
import type { Neighbour } from '../../api/neighbours';
import type { EditNeighbourDialogProps } from './types';

const validateMAC = (value: string): string | undefined => {
    if (!value.trim()) return undefined; // optional
    if (!macToUint64(value)) return 'Invalid MAC address (expected xx:xx:xx:xx:xx:xx)';
    return undefined;
};

const renderMAC = (addr?: Neighbour['link_addr']): string => {
    if (addr?.addr === undefined) return '';
    return formatMACAddress(addr.addr);
};

export const EditNeighbourDialog: React.FC<EditNeighbourDialogProps> = ({
    open,
    onClose,
    onConfirm,
    neighbour,
    table,
}) => {
    const [nextHop, setNextHop] = useState('');
    const [linkAddr, setLinkAddr] = useState('');
    const [hardwareAddr, setHardwareAddr] = useState('');
    const [device, setDevice] = useState('');
    const [priority, setPriority] = useState('');
    const [isSubmitting, setIsSubmitting] = useState(false);

    useEffect(() => {
        if (open && neighbour) {
            setNextHop(neighbour.next_hop || '');
            setLinkAddr(renderMAC(neighbour.link_addr));
            setHardwareAddr(renderMAC(neighbour.hardware_addr));
            setDevice(neighbour.device || '');
            setPriority(neighbour.priority?.toString() || '');
            setIsSubmitting(false);
        }
    }, [open, neighbour]);

    const linkAddrError = validateMAC(linkAddr);
    const hardwareAddrError = validateMAC(hardwareAddr);
    const canSubmit = !isSubmitting && !!nextHop.trim() && !linkAddrError && !hardwareAddrError;

    const handleConfirm = useCallback(async () => {
        if (!canSubmit) return;

        setIsSubmitting(true);
        try {
            const entry: Neighbour = {
                next_hop: nextHop.trim(),
                device: device.trim() || undefined,
                priority: priority.trim() ? Number(priority.trim()) : undefined,
            };

            const linkMac = macToUint64(linkAddr);
            if (linkMac) {
                entry.link_addr = { addr: linkMac };
            }

            const hwMac = macToUint64(hardwareAddr);
            if (hwMac) {
                entry.hardware_addr = { addr: hwMac };
            }

            await onConfirm(table, entry);
            onClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [canSubmit, nextHop, linkAddr, hardwareAddr, device, priority, table, onConfirm, onClose]);

    useDialogKeyboardShortcut({ open, canSubmit, onConfirm: handleConfirm });

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption={`Edit Neighbour — ${table}`} />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: 16, width: 500 }}>
                    <FormField label="Next Hop" required hint="IP address (read-only for existing entry)">
                        <TextInput
                            value={nextHop}
                            onUpdate={setNextHop}
                            disabled
                        />
                    </FormField>

                    <FormField label="Neighbour MAC" hint="Link-layer address (xx:xx:xx:xx:xx:xx)">
                        <TextInput
                            value={linkAddr}
                            onUpdate={setLinkAddr}
                            placeholder="e.g., 52:54:00:12:34:56"
                            validationState={linkAddrError ? 'invalid' : undefined}
                            errorMessage={linkAddrError}
                            autoFocus
                        />
                    </FormField>

                    <FormField label="Interface MAC" hint="Local interface MAC address">
                        <TextInput
                            value={hardwareAddr}
                            onUpdate={setHardwareAddr}
                            placeholder="e.g., 52:54:00:12:34:56"
                            validationState={hardwareAddrError ? 'invalid' : undefined}
                            errorMessage={hardwareAddrError}
                        />
                    </FormField>

                    <FormField label="Device" hint="Network interface name">
                        <TextInput
                            value={device}
                            onUpdate={setDevice}
                            placeholder="e.g., eth0"
                        />
                    </FormField>

                    <FormField label="Priority" hint="Lower value = higher priority (0 uses table default)">
                        <TextInput
                            value={priority}
                            onUpdate={setPriority}
                            placeholder="e.g., 100"
                            type="number"
                        />
                    </FormField>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={handleConfirm}
                onClickButtonCancel={onClose}
                textButtonApply="Save"
                textButtonCancel="Cancel"
                propsButtonApply={{
                    view: 'action' as const,
                    disabled: !canSubmit,
                    loading: isSubmitting,
                }}
            />
        </Dialog>
    );
};
