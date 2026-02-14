import React, { useState, useCallback } from 'react';
import { Box, Dialog, Text } from '@gravity-ui/uikit';
import { useDialogKeyboardShortcut } from '../../hooks';
import type { DeleteRuleDialogProps } from './types';
import './forward.scss';

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
    useDialogKeyboardShortcut({ open, canSubmit, onConfirm: handleConfirm });

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
