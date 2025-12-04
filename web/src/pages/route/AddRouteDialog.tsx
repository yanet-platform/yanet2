import React from 'react';
import { Box, Text, Dialog, TextInput, Switch } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import type { AddRouteDialogProps } from './types';

export const AddRouteDialog: React.FC<AddRouteDialogProps> = ({
    open,
    onClose,
    onConfirm,
    form,
    onFormChange,
    validatePrefix: validatePrefixFn,
    validateNexthop: validateNexthopFn,
}) => {
    const prefixError = validatePrefixFn(form.prefix);
    const nexthopError = validateNexthopFn(form.nexthopAddr);
    const configNameError = !form.configName.trim() ? 'Config name is required' : undefined;

    const handleConfigNameChange = (value: string): void => {
        onFormChange({ ...form, configName: value });
    };

    const handlePrefixChange = (value: string): void => {
        onFormChange({ ...form, prefix: value });
    };

    const handleNexthopChange = (value: string): void => {
        onFormChange({ ...form, nexthopAddr: value });
    };

    const handleDoFlushChange = (checked: boolean): void => {
        onFormChange({ ...form, doFlush: checked });
    };

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Add Route" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px', width: '600px', maxWidth: '90vw' }}>
                    <FormField
                        label="Config Name"
                        required
                        hint="Name of the route module configuration"
                    >
                        <TextInput
                            value={form.configName}
                            onUpdate={handleConfigNameChange}
                            placeholder="Enter config name"
                            style={{ width: '100%' }}
                            validationState={configNameError ? 'invalid' : undefined}
                            errorMessage={configNameError}
                        />
                    </FormField>

                    <FormField
                        label="Prefix (CIDR)"
                        required
                        hint="Format: IP address/prefix length (e.g., 192.168.1.0/24)"
                    >
                        <TextInput
                            value={form.prefix}
                            onUpdate={handlePrefixChange}
                            placeholder="192.168.1.0/24 or 2001:db8::/32"
                            style={{ width: '100%' }}
                            validationState={prefixError ? 'invalid' : undefined}
                            errorMessage={prefixError}
                        />
                    </FormField>

                    <FormField
                        label="Next Hop"
                        required
                        hint="IP address of the next hop (IPv4 or IPv6)"
                    >
                        <TextInput
                            value={form.nexthopAddr}
                            onUpdate={handleNexthopChange}
                            placeholder="192.168.1.1 or 2001:db8::1"
                            style={{ width: '100%' }}
                            validationState={nexthopError ? 'invalid' : undefined}
                            errorMessage={nexthopError}
                        />
                    </FormField>

                    <Box>
                        <Box style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
                            <Switch
                                checked={form.doFlush}
                                onUpdate={handleDoFlushChange}
                            />
                            <Text variant="body-1">Flush RIB to FIB</Text>
                        </Box>
                    </Box>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={onConfirm}
                onClickButtonCancel={onClose}
                textButtonApply="Add"
                textButtonCancel="Cancel"
                propsButtonApply={{ view: 'action' as const }}
            />
        </Dialog>
    );
};
