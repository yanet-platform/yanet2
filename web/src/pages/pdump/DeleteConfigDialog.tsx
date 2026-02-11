import React from 'react';
import { ConfirmDialog } from '../../components';

interface DeleteConfigDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => void;
    configName: string;
    loading?: boolean;
}

export const DeleteConfigDialog: React.FC<DeleteConfigDialogProps> = ({
    open,
    onClose,
    onConfirm,
    configName,
    loading = false,
}) => {
    return (
        <ConfirmDialog
            open={open}
            onClose={onClose}
            onConfirm={onConfirm}
            title="Delete Pdump Config"
            message={`Are you sure you want to delete the pdump config "${configName}"?`}
            secondaryMessage="This action cannot be undone."
            confirmText="Delete"
            cancelText="Cancel"
            loading={loading}
            danger
        />
    );
};
