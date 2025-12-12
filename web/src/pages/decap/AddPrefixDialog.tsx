import React, { useState, useCallback, useEffect } from 'react';
import { Box, Dialog, TextArea, Text } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import type { AddPrefixDialogProps } from './types';
import { parseCIDRPrefix, CIDRParseError } from '../../utils';

export const AddPrefixDialog: React.FC<AddPrefixDialogProps> = ({
    open,
    onClose,
    onConfirm,
}) => {
    const [prefixesInput, setPrefixesInput] = useState<string>('');
    const [isSubmitting, setIsSubmitting] = useState<boolean>(false);

    const handleClose = useCallback(() => {
        setPrefixesInput('');
        onClose();
    }, [onClose]);

    const parsePrefixes = useCallback((): { valid: string[]; errors: string[] } => {
        const lines = prefixesInput
            .split(/[\n,;]/)
            .map((line) => line.trim())
            .filter((line) => line.length > 0);

        const valid: string[] = [];
        const errors: string[] = [];

        for (const line of lines) {
            const result = parseCIDRPrefix(line);
            if (result.ok) {
                valid.push(line);
            } else {
                let errorMsg = `"${line}": `;
                switch (result.error) {
                    case CIDRParseError.InvalidFormat:
                        errorMsg += 'invalid CIDR format';
                        break;
                    case CIDRParseError.InvalidPrefixLength:
                        errorMsg += 'invalid prefix length';
                        break;
                    case CIDRParseError.InvalidIPAddress:
                        errorMsg += 'invalid IP address';
                        break;
                    default:
                        errorMsg += 'unknown error';
                }
                errors.push(errorMsg);
            }
        }

        return { valid, errors };
    }, [prefixesInput]);

    const handleConfirm = useCallback(async () => {
        const { valid, errors } = parsePrefixes();

        if (errors.length > 0) {
            return;
        }

        if (valid.length === 0) {
            return;
        }

        setIsSubmitting(true);
        try {
            await onConfirm(valid);
            handleClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [parsePrefixes, onConfirm, handleClose]);

    const { valid, errors } = parsePrefixes();
    const hasErrors = errors.length > 0;
    const isEmpty = prefixesInput.trim().length === 0;
    const canSubmit = !hasErrors && !isEmpty && !isSubmitting;

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
            <Dialog.Header caption="Add Prefixes" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px', width: '500px', maxWidth: '90vw' }}>
                    <FormField
                        label="Prefixes (CIDR)"
                        required
                        hint="Enter one or more prefixes in CIDR notation, one per line or separated by commas. Press Ctrl+Enter to save."
                    >
                        <TextArea
                            value={prefixesInput}
                            onUpdate={setPrefixesInput}
                            placeholder="192.168.1.0/24&#10;10.0.0.0/8&#10;2001:db8::/32"
                            rows={6}
                            style={{ width: '100%' }}
                            validationState={hasErrors ? 'invalid' : undefined}
                        />
                    </FormField>

                    {hasErrors && (
                        <Box style={{ color: 'var(--g-color-text-danger)' }}>
                            <Text variant="body-1">Invalid prefixes:</Text>
                            <Box component="ul" style={{ margin: '8px 0', paddingLeft: '20px' }}>
                                {errors.map((error, idx) => (
                                    <li key={idx}>
                                        <Text variant="body-1">{error}</Text>
                                    </li>
                                ))}
                            </Box>
                        </Box>
                    )}

                    {!hasErrors && valid.length > 0 && (
                        <Text variant="body-1" color="secondary">
                            {valid.length} valid prefix(es) will be added
                        </Text>
                    )}
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
