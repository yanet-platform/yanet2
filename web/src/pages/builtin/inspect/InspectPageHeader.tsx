import React from 'react';
import { Button } from '@gravity-ui/uikit';
import { ArrowsRotateRight } from '@gravity-ui/icons';
import { PageHeader } from '../../../components';

export interface InspectPageHeaderProps {
    onRefresh: () => void;
    refreshing?: boolean;
}

/** Page header for the Inspect (HUD) page with a refresh button. */
export const InspectPageHeader: React.FC<InspectPageHeaderProps> = ({
    onRefresh,
    refreshing = false,
}) => (
    <PageHeader
        title="Inspect"
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
