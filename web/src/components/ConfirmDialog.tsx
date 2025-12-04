import React from 'react';
import { Box, Text, Dialog } from '@gravity-ui/uikit';

export interface ConfirmDialogProps {
    /** Whether the dialog is open */
    open: boolean;
    /** Handler for closing the dialog */
    onClose: () => void;
    /** Handler for confirming the action */
    onConfirm: () => void;
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
    /** Whether the confirm action is loading */
    loading?: boolean;
    /** Whether to use danger styling for confirm button */
    danger?: boolean;
    /** Optional content to render below the messages */
    children?: React.ReactNode;
}

/**
 * Reusable confirmation dialog component
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
    loading = false,
    danger = false,
    children,
}) => {
    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption={title} />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '12px', minWidth: '320px' }}>
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
                onClickButtonApply={onConfirm}
                textButtonApply={confirmText}
                textButtonCancel={cancelText}
                loading={loading}
                propsButtonApply={danger ? { view: 'outlined-danger' as const } : undefined}
            />
        </Dialog>
    );
};

