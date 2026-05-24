import React from 'react';
import { Button } from '@gravity-ui/uikit';
import { ArrowsRotateRight } from '@gravity-ui/icons';
import { PageHeader } from '../../../components';

export interface Inspect3dPageHeaderProps {
    onRefresh: () => void;
    refreshing?: boolean;
}

/** Page header for the Inspect (3D) page with a refresh button. */
export const Inspect3dPageHeader: React.FC<Inspect3dPageHeaderProps> = ({
    onRefresh,
    refreshing = false,
}) => (
    <PageHeader
        title="Inspect (3D)"
        actions={
            <Button view="outlined" size="m" onClick={onRefresh} loading={refreshing}>
                <Button.Icon>
                    <ArrowsRotateRight />
                </Button.Icon>
                Refresh
            </Button>
        }
    />
);
