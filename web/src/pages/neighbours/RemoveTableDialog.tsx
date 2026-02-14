import React, { useCallback } from 'react';
import { ConfirmDialog } from '../../components';
import type { RemoveTableDialogProps } from './types';

export const RemoveTableDialog: React.FC<RemoveTableDialogProps> = ({
    open,
    onClose,
    onConfirm,
    tableName,
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
            title="Remove Table"
            message={
                <>
                    Are you sure you want to remove table <strong>{tableName}</strong> and all its entries?
                </>
            }
            secondaryMessage="Press Ctrl+Enter to confirm."
            confirmText="Remove"
            danger
        />
    );
};
