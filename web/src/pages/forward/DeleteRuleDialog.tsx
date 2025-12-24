import React, { useState, useCallback, useEffect } from 'react';
import { Box, Dialog, Text } from '@gravity-ui/uikit';
import type { DeleteRuleDialogProps } from './types';
import './forward.css';

export const DeleteRuleDialog: React.FC<DeleteRuleDialogProps> = ({
    open,
    onClose,
    onConfirm,
    selectedCount,
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

    const canSubmit = !isSubmitting && selectedCount > 0;

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
            <Dialog.Header caption="Delete Rules" />
            <Dialog.Body>
                <Box className="forward-dialog__body">
                    <Text variant="body-1">
                        Are you sure you want to delete {selectedCount} rule(s)?
                    </Text>
                    <Text variant="body-1" color="secondary">
                        Press Ctrl+Enter to confirm.
                    </Text>
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
