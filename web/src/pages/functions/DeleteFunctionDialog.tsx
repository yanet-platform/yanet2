import React from 'react';
import { ConfirmDialog } from '../../components';

export interface DeleteFunctionDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => void;
    functionName: string;
    loading: boolean;
}

export const DeleteFunctionDialog: React.FC<DeleteFunctionDialogProps> = ({
    open,
    onClose,
    onConfirm,
    functionName,
    loading,
}) => {
    return (
        <ConfirmDialog
            open={open}
            onClose={onClose}
            onConfirm={onConfirm}
            title="Delete Function"
            message={
                <>
                    Are you sure you want to delete function{' '}
                    <span style={{ fontWeight: 600 }}>{functionName}</span>?
                </>
            }
            secondaryMessage="This action cannot be undone and the pipeline using this function may stop working."
            confirmText="Delete"
            cancelText="Cancel"
            loading={loading}
            danger
        />
    );
};

