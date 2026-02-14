import React from 'react';
import { Box, Text, Dialog } from '@gravity-ui/uikit';
import { useDialogKeyboardShortcut } from '../../hooks';
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
    const canSubmit = count > 0;

    useDialogKeyboardShortcut({ open, canSubmit, onConfirm });

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Delete Routes" />
            <Dialog.Body>
                <Box className="delete-route-dialog__message">
                    <Text variant="body-1">
                        Are you sure you want to delete {count} {formatRouteCount(count)}? Press Ctrl+Enter to confirm.
                    </Text>
                </Box>
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
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={onConfirm}
                onClickButtonCancel={onClose}
                textButtonApply="Delete"
                textButtonCancel="Cancel"
                propsButtonApply={{
                    view: 'outlined-danger' as const,
                    disabled: !canSubmit,
                }}
            />
        </Dialog>
    );
};
