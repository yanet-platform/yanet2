import React, { useEffect } from 'react';
import { Dialog, Box, Text, Button } from '@gravity-ui/uikit';

export interface DeletePipelineDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => void;
    pipelineName: string;
    loading?: boolean;
}

export const DeletePipelineDialog: React.FC<DeletePipelineDialogProps> = ({
    open,
    onClose,
    onConfirm,
    pipelineName,
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
            <Dialog.Header caption="Delete Pipeline" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '12px', minWidth: '320px' }}>
                    <Text variant="body-1">
                        Are you sure you want to delete pipeline "{pipelineName}"?
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

