import React from 'react';
import { Box, Text, Dialog } from '@gravity-ui/uikit';
import { RouteListItem } from './RouteListItem';
import { formatRouteCount } from './utils';
import type { DeleteRouteDialogProps } from './types';

export const DeleteRouteDialog: React.FC<DeleteRouteDialogProps> = ({
    open,
    onClose,
    onConfirm,
    selectedRoutes,
}) => {
    const count = selectedRoutes.length;

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Удаление маршрутов" />
            <Dialog.Body>
                <Box style={{ marginBottom: '16px' }}>
                    <Text variant="body-1">
                        Вы уверены, что хотите удалить {count} {formatRouteCount(count)}?
                    </Text>
                </Box>
                {selectedRoutes.length > 0 && (
                    <Box style={{ maxHeight: '300px', overflowY: 'auto', marginTop: '16px' }}>
                        <Text variant="subheader-2" style={{ marginBottom: '8px' }}>
                            Выбранные маршруты:
                        </Text>
                        <Box style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
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
                textButtonApply="Удалить"
                textButtonCancel="Отмена"
                propsButtonApply={{ view: 'outlined-danger' as const }}
            />
        </Dialog>
    );
};
