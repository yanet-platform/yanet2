import React, { useCallback } from 'react';
import { ConfirmDialog } from '../../components';
import type { RemoveNeighboursDialogProps } from './types';

export const RemoveNeighboursDialog: React.FC<RemoveNeighboursDialogProps> = ({
    open,
    onClose,
    onConfirm,
    selectedCount,
}) => {
    const handleConfirm = useCallback(async () => {
        await onConfirm();
        onClose();
    }, [onConfirm, onClose]);

    return (
        <ConfirmDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Remove Neighbours"
            message={`Are you sure you want to remove ${selectedCount} neighbour(s)? Press Ctrl+Enter to confirm.`}
            confirmText="Remove"
            danger
            disabled={selectedCount === 0}
        />
    );
};
