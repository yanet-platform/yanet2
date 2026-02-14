import React, { useState, useCallback } from 'react';
import { Box, Dialog, Text } from '@gravity-ui/uikit';
import { useDialogKeyboardShortcut } from '../../hooks';
import type { RemoveTableDialogProps } from './types';

export const RemoveTableDialog: React.FC<RemoveTableDialogProps> = ({
    open,
    onClose,
    onConfirm,
    tableName,
}) => {
    const [isSubmitting, setIsSubmitting] = useState(false);

    const handleConfirm = useCallback(async () => {
        setIsSubmitting(true);
        try {
            await onConfirm();
            onClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [onConfirm, onClose]);

    const canSubmit = !isSubmitting;

    useDialogKeyboardShortcut({ open, canSubmit, onConfirm: handleConfirm });

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Remove Table" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    <Text variant="body-1">
                        Are you sure you want to remove table <strong>{tableName}</strong> and all its entries?
                    </Text>
                    <Text variant="body-1" color="secondary">
                        Press Ctrl+Enter to confirm.
                    </Text>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={handleConfirm}
                onClickButtonCancel={onClose}
                textButtonApply="Remove"
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
