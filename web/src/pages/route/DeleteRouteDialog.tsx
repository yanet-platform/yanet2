import React, { useCallback } from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { ConfirmDialog } from '../../components';
import { RouteListItem } from './RouteListItem';
import { formatRouteCount } from './utils';
import type { DeleteRouteDialogProps } from './types';
import './route.scss';

export const DeleteRouteDialog: React.FC<DeleteRouteDialogProps> = ({
    open,
    onClose,
    onConfirm,
    selectedRoutes,
}) => {
    const count = selectedRoutes.length;

    const handleConfirm = useCallback(async () => {
        await onConfirm();
        onClose();
    }, [onConfirm, onClose]);

    return (
        <ConfirmDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Delete Routes"
            message={`Are you sure you want to delete ${count} ${formatRouteCount(count)}? Press Ctrl+Enter to confirm.`}
            confirmText="Delete"
            danger
            disabled={count === 0}
        >
            {selectedRoutes.length > 0 && (
                <Box className="delete-route-dialog__list">
                    <Text variant="subheader-2" className="delete-route-dialog__list-header">
                        Selected routes:
                    </Text>
                    <Box className="delete-route-dialog__list-items">
                        {selectedRoutes.map((route, idx) => (
                            <RouteListItem key={idx} route={route} />
                        ))}
                    </Box>
                </Box>
            )}
        </ConfirmDialog>
    );
};
