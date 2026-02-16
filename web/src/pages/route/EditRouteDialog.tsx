import React, { useState, useCallback, useEffect } from 'react';
import { Box, Text, Dialog, TextInput, Switch } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import { useDialogKeyboardShortcut } from '../../hooks';
import type { EditRouteDialogProps } from './types';
import './route.scss';

export const EditRouteDialog: React.FC<EditRouteDialogProps> = ({
    open,
    onClose,
    onConfirm,
    route,
    configName,
    validatePrefix: validatePrefixFn,
    validateNexthop: validateNexthopFn,
}) => {
    const [prefix, setPrefix] = useState('');
    const [nexthopAddr, setNexthopAddr] = useState('');
    const [doFlush, setDoFlush] = useState(false);
    const [isSubmitting, setIsSubmitting] = useState(false);

    useEffect(() => {
        if (open && route) {
            setPrefix(route.prefix || '');
            setNexthopAddr(route.next_hop || '');
            setDoFlush(false);
            setIsSubmitting(false);
        }
    }, [open, route]);

    const prefixError = validatePrefixFn(prefix);
    const nexthopError = validateNexthopFn(nexthopAddr);
    const canSubmit = !prefixError && !nexthopError && !isSubmitting;

    const handleConfirm = useCallback(async () => {
        if (!canSubmit) return;

        setIsSubmitting(true);
        try {
            await onConfirm(prefix, nexthopAddr, doFlush);
            onClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [canSubmit, prefix, nexthopAddr, doFlush, onConfirm, onClose]);

    useDialogKeyboardShortcut({ open, canSubmit, onConfirm: handleConfirm });

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption={`Edit Route — ${configName}`} />
            <Dialog.Body>
                <Box className="add-route-dialog__body">
                    <FormField
                        label="Prefix (CIDR)"
                        required
                        hint="Format: IP address/prefix length (e.g., 192.168.1.0/24)"
                    >
                        <TextInput
                            value={prefix}
                            onUpdate={setPrefix}
                            placeholder="192.168.1.0/24 or 2001:db8::/32"
                            validationState={prefixError ? 'invalid' : undefined}
                            errorMessage={prefixError}
                            autoFocus
                        />
                    </FormField>

                    <FormField
                        label="Next Hop"
                        required
                        hint="IP address of the next hop (IPv4 or IPv6)"
                    >
                        <TextInput
                            value={nexthopAddr}
                            onUpdate={setNexthopAddr}
                            placeholder="192.168.1.1 or 2001:db8::1"
                            validationState={nexthopError ? 'invalid' : undefined}
                            errorMessage={nexthopError}
                        />
                    </FormField>

                    <Box>
                        <Box className="add-route-dialog__switch-row">
                            <Switch
                                checked={doFlush}
                                onUpdate={setDoFlush}
                            />
                            <Text variant="body-1">Flush RIB to FIB</Text>
                        </Box>
                    </Box>
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
