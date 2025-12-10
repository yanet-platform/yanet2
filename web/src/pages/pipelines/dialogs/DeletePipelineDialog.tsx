import React from 'react';
import { ConfirmDialog } from '../../../components';

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
    return (
        <ConfirmDialog
            open={open}
            onClose={onClose}
            onConfirm={onConfirm}
            title="Delete Pipeline"
            message={`Are you sure you want to delete pipeline "${pipelineName}"?`}
            secondaryMessage="This action cannot be undone."
            confirmText="Delete"
            loading={loading}
            danger
        />
    );
};
