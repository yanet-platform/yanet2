import React, { useCallback } from 'react';
import { Dialog, Box } from '@gravity-ui/uikit';
import { useDialogKeyboardShortcut } from '../hooks';

export interface FormDialogProps {
    /** Whether the dialog is open */
    open: boolean;
    /** Handler for closing the dialog */
    onClose: () => void;
    /** Handler for confirming/submitting the form */
    onConfirm: () => void;
    /** Dialog title */
    title: string;
    /** Confirm button text */
    confirmText?: string;
    /** Cancel button text */
    cancelText?: string;
    /** Whether the confirm action is loading */
    loading?: boolean;
    /** Whether to disable the confirm button */
    disabled?: boolean;
    /** Whether to show cancel button */
    showCancel?: boolean;
    /** Dialog body content (form fields) */
    children: React.ReactNode;
    /** Optional width for the dialog body */
    width?: string | number;
}

/**
 * Reusable form dialog component with Ctrl+Enter/Cmd+Enter support
 */
export const FormDialog: React.FC<FormDialogProps> = ({
    open,
    onClose,
    onConfirm,
    title,
    confirmText = 'Confirm',
    cancelText = 'Cancel',
    loading = false,
    disabled = false,
    showCancel = true,
    children,
    width = '400px',
}) => {
    const canSubmit = !loading && !disabled;

    const handleConfirm = useCallback(() => {
        if (canSubmit) {
            onConfirm();
        }
    }, [canSubmit, onConfirm]);

    // Add Ctrl+Enter keyboard shortcut
    useDialogKeyboardShortcut({ open, canSubmit, onConfirm });
    
    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption={title} />
            <Dialog.Body>
                <Box style={{ width, maxWidth: '90vw' }}>
                    {children}
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={handleConfirm}
                onClickButtonCancel={showCancel ? onClose : undefined}
                textButtonApply={confirmText}
                textButtonCancel={showCancel ? cancelText : undefined}
                loading={loading}
                propsButtonApply={{
                    view: 'action' as const,
                    disabled: !canSubmit,
                }}
            />
        </Dialog>
    );
};

