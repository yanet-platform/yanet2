import React, { useEffect, useCallback } from 'react';
import { Dialog, Box } from '@gravity-ui/uikit';

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
    showCancel = true,
    children,
    width = '400px',
}) => {
    const handleConfirm = useCallback(() => {
        if (!loading) {
            onConfirm();
        }
    }, [loading, onConfirm]);

    // Handle Ctrl+Enter / Cmd+Enter keyboard shortcut
    useEffect(() => {
        if (!open) return;
        
        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                e.preventDefault();
                handleConfirm();
            }
        };
        
        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [open, handleConfirm]);
    
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
                propsButtonApply={{ view: 'action' as const }}
            />
        </Dialog>
    );
};

