import React from 'react';
import { Button } from '@gravity-ui/uikit';
import { PageHeader } from '../../../components';

export interface DecapPageHeaderProps {
    onAddPrefix: () => void;
    onDeletePrefixes: () => void;
    isDeleteDisabled: boolean;
}

export const DecapPageHeader: React.FC<DecapPageHeaderProps> = ({
    onAddPrefix,
    onDeletePrefixes,
    isDeleteDisabled,
}) => (
    <PageHeader
        title="Decap"
        actions={
            <>
                <Button view="action" onClick={onAddPrefix}>
                    Add Prefix
                </Button>
                <Button
                    view="outlined-danger"
                    onClick={onDeletePrefixes}
                    disabled={isDeleteDisabled}
                >
                    Delete Prefixes
                </Button>
            </>
        }
    />
);
