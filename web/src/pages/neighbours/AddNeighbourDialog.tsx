import React, { useState, useCallback, useEffect } from 'react';
import { Box, Dialog, TextInput, Select } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import { useDialogKeyboardShortcut } from '../../hooks';
import { macToUint64 } from '../../utils/mac';
import type { Neighbour } from '../../api/neighbours';
import type { AddNeighbourDialogProps } from './types';

const validateNextHop = (value: string): string | undefined => {
    if (!value.trim()) return 'Next Hop is required';
    return undefined;
};

const validateMAC = (value: string): string | undefined => {
    if (!value.trim()) return undefined; // optional
    if (!macToUint64(value)) return 'Invalid MAC address (expected xx:xx:xx:xx:xx:xx)';
    return undefined;
};

export const AddNeighbourDialog: React.FC<AddNeighbourDialogProps> = ({
    open,
    onClose,
    onConfirm,
    tables,
    defaultTable,
}) => {
    const [table, setTable] = useState<string[]>([defaultTable]);
    const [nextHop, setNextHop] = useState('');
    const [linkAddr, setLinkAddr] = useState('');
    const [hardwareAddr, setHardwareAddr] = useState('');
    const [device, setDevice] = useState('');
    const [priority, setPriority] = useState('');
    const [isSubmitting, setIsSubmitting] = useState(false);

    useEffect(() => {
        if (open) {
            setTable([defaultTable]);
            setNextHop('');
            setLinkAddr('');
            setHardwareAddr('');
            setDevice('');
            setPriority('');
            setIsSubmitting(false);
        }
    }, [open, defaultTable]);

    const tableOptions = tables
        .filter((t) => t.name)
        .map((t) => ({ value: t.name!, content: t.name! }));

    const nextHopError = nextHop ? validateNextHop(nextHop) : undefined;
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

            await onConfirm(table[0], entry);
            onClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [canSubmit, nextHop, linkAddr, hardwareAddr, device, priority, table, onConfirm, onClose]);

    useDialogKeyboardShortcut({ open, canSubmit, onConfirm: handleConfirm });

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Add Neighbour" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: 16, width: 500 }}>
                    <FormField label="Table" required hint="Target table for the new entry">
                        <Select
                            value={table}
                            onUpdate={setTable}
                            options={tableOptions}
                        />
                    </FormField>

                    <FormField label="Next Hop" required hint="IP address (IPv4 or IPv6)">
                        <TextInput
                            value={nextHop}
                            onUpdate={setNextHop}
                            placeholder="e.g., 192.168.1.1 or fe80::1"
                            validationState={nextHopError ? 'invalid' : undefined}
                            errorMessage={nextHopError}
                            autoFocus
                        />
                    </FormField>

                    <FormField label="Neighbour MAC" hint="Link-layer address (xx:xx:xx:xx:xx:xx)">
                        <TextInput
                            value={linkAddr}
                            onUpdate={setLinkAddr}
                            placeholder="e.g., 52:54:00:12:34:56"
                            validationState={linkAddrError ? 'invalid' : undefined}
                            errorMessage={linkAddrError}
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
                textButtonApply="Add"
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
