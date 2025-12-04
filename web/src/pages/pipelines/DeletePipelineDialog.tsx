import React from 'react';
import { ConfirmDialog } from '../../components';

export interface DeletePipelineDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => void;
    pipelineName: string;
    loading: boolean;
}

export const DeletePipelineDialog: React.FC<DeletePipelineDialogProps> = ({
    open,
    onClose,
    onConfirm,
    pipelineName,
    loading,
}) => {
    return (
        <ConfirmDialog
            open={open}
            onClose={onClose}
            onConfirm={onConfirm}
            title="Delete Pipeline"
            message={
                <>
                    Are you sure you want to delete pipeline{' '}
                    <span style={{ fontWeight: 600 }}>{pipelineName}</span>?
                </>
            }
            secondaryMessage="This action cannot be undone and the devices using this pipeline may stop working."
            confirmText="Delete"
            cancelText="Cancel"
            loading={loading}
            danger
        />
    );
};

