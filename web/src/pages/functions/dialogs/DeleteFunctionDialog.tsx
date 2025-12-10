import React from 'react';
import { ConfirmDialog } from '../../../components';

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
    return (
        <ConfirmDialog
            open={open}
            onClose={onClose}
            onConfirm={onConfirm}
            title="Delete Function"
            message={`Are you sure you want to delete function "${functionName}"?`}
            secondaryMessage="This action cannot be undone."
            confirmText="Delete"
            loading={loading}
            danger
        />
    );
};
