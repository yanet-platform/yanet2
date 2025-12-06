import React, { useCallback, useEffect } from 'react';
import { Dialog, Box, Text, Button } from '@gravity-ui/uikit';

export interface DeleteFunctionDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => void;
    functionName: string;
    loading?: boolean;
}

export const DeleteFunctionDialog: React.FC<DeleteFunctionDialogProps> = ({
    open,
    onClose,
    onConfirm,
    functionName,
    loading = false,
}) => {
    useEffect(() => {
        if (!open) return;
        
        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                onConfirm();
            }
        };
        
        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [open, onConfirm]);

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Delete Function" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '12px', minWidth: '320px' }}>
                    <Text variant="body-1">
                        Are you sure you want to delete function "{functionName}"?
                    </Text>
                    <Text variant="body-2" color="secondary">
                        This action cannot be undone.
                    </Text>
                </Box>
            </Dialog.Body>
            <Dialog.Footer>
                <Button
                    view="outlined-danger"
                    onClick={onConfirm}
                    loading={loading}
                >
                    Delete
                </Button>
            </Dialog.Footer>
        </Dialog>
    );
};
