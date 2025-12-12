import React, { useState, useCallback, useEffect } from 'react';
import { Box, Dialog, TextInput } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import type { AddPrefixDialogProps } from './types';
import { parseCIDRPrefix, CIDRParseError } from '../../utils';

export const AddPrefixDialog: React.FC<AddPrefixDialogProps> = ({
    open,
    onClose,
    onConfirm,
}) => {
    const [prefixInput, setPrefixInput] = useState<string>('');
    const [isSubmitting, setIsSubmitting] = useState<boolean>(false);

    const handleClose = useCallback(() => {
        setPrefixInput('');
        onClose();
    }, [onClose]);

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
        const trimmed = prefixInput.trim();
        if (!trimmed) return;

        const error = validatePrefix();
        if (error) return;

        setIsSubmitting(true);
        try {
            await onConfirm([trimmed]);
            handleClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [prefixInput, validatePrefix, onConfirm, handleClose]);

    const error = validatePrefix();
    const isEmpty = prefixInput.trim().length === 0;
    const canSubmit = !error && !isEmpty && !isSubmitting;

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
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px', width: '400px', maxWidth: '90vw' }}>
                    <FormField
                        label="Prefix (CIDR)"
                        required
                        hint="Enter prefix in CIDR notation. Press Ctrl+Enter to save."
                    >
                        <TextInput
                            value={prefixInput}
                            onUpdate={setPrefixInput}
                            placeholder="192.168.1.0/24 or 2001:db8::/32"
                            style={{ width: '100%' }}
                            validationState={error ? 'invalid' : undefined}
                            errorMessage={error}
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
