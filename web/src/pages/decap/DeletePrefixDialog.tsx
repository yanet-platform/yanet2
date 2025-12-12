import React, { useState, useCallback, useEffect } from 'react';
import { Box, Dialog, Text } from '@gravity-ui/uikit';
import type { DeletePrefixDialogProps } from './types';

export const DeletePrefixDialog: React.FC<DeletePrefixDialogProps> = ({
    open,
    onClose,
    onConfirm,
    selectedPrefixes,
}) => {
    const [isSubmitting, setIsSubmitting] = useState<boolean>(false);

    const handleConfirm = useCallback(async () => {
        setIsSubmitting(true);
        try {
            await onConfirm();
            onClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [onConfirm, onClose]);

    const prefixCount = selectedPrefixes.length;
    const maxDisplayed = 10;
    const displayedPrefixes = selectedPrefixes.slice(0, maxDisplayed);
    const remainingCount = prefixCount - maxDisplayed;
    const canSubmit = !isSubmitting && prefixCount > 0;

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
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Delete Prefixes" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px', width: '400px', maxWidth: '90vw' }}>
                    <Text variant="body-1">
                        Are you sure you want to delete {prefixCount} prefix(es)? Press Ctrl+Enter to confirm.
                    </Text>

                    <Box style={{
                        backgroundColor: 'var(--g-color-base-generic)',
                        borderRadius: '8px',
                        padding: '12px',
                        maxHeight: '200px',
                        overflow: 'auto',
                    }}>
                        <Box component="ul" style={{ margin: 0, paddingLeft: '20px' }}>
                            {displayedPrefixes.map((prefix, idx) => (
                                <li key={idx}>
                                    <Text variant="code-1">{prefix}</Text>
                                </li>
                            ))}
                            {remainingCount > 0 && (
                                <li>
                                    <Text variant="body-1" color="secondary">
                                        ... and {remainingCount} more
                                    </Text>
                                </li>
                            )}
                        </Box>
                    </Box>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={handleConfirm}
                onClickButtonCancel={onClose}
                textButtonApply="Delete"
                textButtonCancel="Cancel"
                propsButtonApply={{
                    view: 'outlined-danger' as const,
                    disabled: !canSubmit,
                    loading: isSubmitting,
                }}
            />
        </Dialog>
    );
};
