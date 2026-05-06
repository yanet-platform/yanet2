import React from 'react';
import { Button } from '@gravity-ui/uikit';
import { ArrowsRotateRight } from '@gravity-ui/icons';

export interface InspectPageHeaderProps {
    onRefresh: () => void;
    refreshing?: boolean;
}

export const InspectPageHeader: React.FC<InspectPageHeaderProps> = ({
    onRefresh,
    refreshing = false,
}) => {
    return (
        <div className="inspect-page-header">
            <div>
                <h1 className="inspect-page-title">Inspect</h1>
                <div className="inspect-page-sub">
                    Live view of dataplane modules, devices, pipelines and functions.
                </div>
            </div>
            <div className="inspect-page-actions">
                <Button view="outlined" size="m" onClick={onRefresh} loading={refreshing}>
                    <Button.Icon>
                        <ArrowsRotateRight />
                    </Button.Icon>
                    Refresh
                </Button>
            </div>
        </div>
    );
};
