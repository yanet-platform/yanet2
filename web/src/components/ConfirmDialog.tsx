import React, { useState, useCallback } from 'react';
import { Box, Text, Dialog } from '@gravity-ui/uikit';
import { useDialogKeyboardShortcut } from '../hooks';
import './common.css';

export interface ConfirmDialogProps {
    /** Whether the dialog is open */
    open: boolean;
    /** Handler for closing the dialog */
    onClose: () => void;
    /** Handler for confirming the action (can be async) */
    onConfirm: () => void | Promise<void>;
    /** Dialog title */
    title: string;
    /** Main message text */
    message: React.ReactNode;
    /** Optional secondary/warning text */
    secondaryMessage?: React.ReactNode;
    /** Confirm button text */
    confirmText?: string;
    /** Cancel button text */
    cancelText?: string;
    /** Whether the confirm action is loading (externally controlled) */
    loading?: boolean;
    /** Whether to use danger styling for confirm button */
    danger?: boolean;
    /** Whether to disable the confirm button */
    disabled?: boolean;
    /** Optional content to render below the messages */
    children?: React.ReactNode;
}

/**
 * Reusable confirmation dialog component with Ctrl+Enter support
 */
export const ConfirmDialog: React.FC<ConfirmDialogProps> = ({
    open,
    onClose,
    onConfirm,
    title,
    message,
    secondaryMessage,
    confirmText = 'Confirm',
    cancelText = 'Cancel',
    loading: externalLoading = false,
    danger = false,
    disabled = false,
    children,
}) => {
    const [internalLoading, setInternalLoading] = useState(false);

    // Use external loading if provided, otherwise use internal
    const isLoading = externalLoading || internalLoading;
    const canSubmit = !disabled && !isLoading;

    const handleConfirm = useCallback(async () => {
        if (!canSubmit) return;

        setInternalLoading(true);
        try {
            await onConfirm();
        } finally {
            setInternalLoading(false);
        }
    }, [onConfirm, canSubmit]);

    // Add Ctrl+Enter keyboard shortcut
    useDialogKeyboardShortcut({ open, canSubmit, onConfirm: handleConfirm });

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption={title} />
            <Dialog.Body>
                <Box className="confirm-dialog__body">
                    <Text variant="body-1">{message}</Text>
                    {secondaryMessage && (
                        <Text variant="body-2" color="secondary">
                            {secondaryMessage}
                        </Text>
                    )}
                    {children}
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleConfirm}
                textButtonApply={confirmText}
                textButtonCancel={cancelText}
                loading={isLoading}
                propsButtonApply={{
                    view: danger ? ('outlined-danger' as const) : undefined,
                    disabled: !canSubmit,
                }}
            />
        </Dialog>
    );
};
