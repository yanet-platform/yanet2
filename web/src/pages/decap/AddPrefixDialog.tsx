import React, { useState, useCallback, useEffect, useMemo } from 'react';
import { Box, Dialog, TextInput } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import type { AddPrefixDialogProps } from './types';
import { parseCIDRPrefix, CIDRParseError } from '../../utils';
import './decap.css';

export const AddPrefixDialog: React.FC<AddPrefixDialogProps> = ({
    open,
    onClose,
    onConfirm,
    existingConfigs,
}) => {
    const [configName, setConfigName] = useState<string>('');
    const [prefixInput, setPrefixInput] = useState<string>('');
    const [isSubmitting, setIsSubmitting] = useState<boolean>(false);

    // Reset form when dialog opens
    useEffect(() => {
        if (open) {
            setConfigName('');
            setPrefixInput('');
            setIsSubmitting(false);
        }
    }, [open]);

    const handleClose = useCallback(() => {
        setConfigName('');
        setPrefixInput('');
        onClose();
    }, [onClose]);

    const validateConfigName = useCallback((): string | undefined => {
        const trimmed = configName.trim();
        if (!trimmed) {
            return 'Config name is required';
        }
        return undefined;
    }, [configName]);

    const validatePrefix = useCallback((): string | undefined => {
        const trimmed = prefixInput.trim();
        if (!trimmed) return undefined;

        const result = parseCIDRPrefix(trimmed);
        if (result.ok) return undefined;

        switch (result.error) {
            case CIDRParseError.InvalidFormat:
                return 'Invalid CIDR format (e.g., 192.168.1.0/24)';
            case CIDRParseError.InvalidPrefixLength:
                return 'Invalid prefix length';
            case CIDRParseError.InvalidIPAddress:
                return 'Invalid IP address';
            default:
                return 'Invalid prefix';
        }
    }, [prefixInput]);

    const handleConfirm = useCallback(async () => {
        const trimmedConfig = configName.trim();
        const trimmedPrefix = prefixInput.trim();
        if (!trimmedConfig || !trimmedPrefix) return;

        const configError = validateConfigName();
        const prefixError = validatePrefix();
        if (configError || prefixError) return;

        setIsSubmitting(true);
        try {
            await onConfirm(trimmedConfig, [trimmedPrefix]);
            handleClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [configName, prefixInput, validateConfigName, validatePrefix, onConfirm, handleClose]);

    const configNameError = validateConfigName();
    const prefixError = validatePrefix();
    const isConfigNameEmpty = configName.trim().length === 0;
    const isPrefixEmpty = prefixInput.trim().length === 0;
    const canSubmit = !configNameError && !prefixError && !isConfigNameEmpty && !isPrefixEmpty && !isSubmitting;

    const isExistingConfig = useMemo(() => {
        return existingConfigs.includes(configName.trim());
    }, [existingConfigs, configName]);

    // Handle Ctrl+Enter / Cmd+Enter
    useEffect(() => {
        if (!open) return;

        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && canSubmit) {
                e.preventDefault();
                handleConfirm();
            }
        };

        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [open, canSubmit, handleConfirm]);

    return (
        <Dialog open={open} onClose={handleClose}>
            <Dialog.Header caption="Add Prefix" />
            <Dialog.Body>
                <Box className="decap-dialog__body">
                    <FormField
                        label="Config Name"
                        required
                        hint={isExistingConfig ? 'Config exists - prefix will be added to it' : 'New config will be created'}
                    >
                        <TextInput
                            value={configName}
                            onUpdate={setConfigName}
                            placeholder="Enter config name"
                            className="decap-dialog__text-input"
                            validationState={!isConfigNameEmpty && configNameError ? 'invalid' : undefined}
                            errorMessage={!isConfigNameEmpty ? configNameError : undefined}
                        />
                    </FormField>
                    <FormField
                        label="Prefix (CIDR)"
                        required
                        hint="Press Ctrl+Enter to save."
                    >
                        <TextInput
                            value={prefixInput}
                            onUpdate={setPrefixInput}
                            placeholder="192.168.1.0/24 or 2001:db8::/32"
                            className="decap-dialog__text-input"
                            validationState={prefixError ? 'invalid' : undefined}
                            errorMessage={prefixError}
                            autoFocus
                        />
                    </FormField>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={handleConfirm}
                onClickButtonCancel={handleClose}
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
