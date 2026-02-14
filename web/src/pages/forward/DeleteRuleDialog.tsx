import React, { useCallback } from 'react';
import { ConfirmDialog } from '../../components';
import type { DeleteRuleDialogProps } from './types';

export const DeleteRuleDialog: React.FC<DeleteRuleDialogProps> = ({
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
            title="Delete Rules"
            message={`Are you sure you want to delete ${selectedCount} rule(s)? Press Ctrl+Enter to confirm.`}
            confirmText="Delete"
            danger
            disabled={selectedCount === 0}
        />
    );
};
