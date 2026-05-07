import React from 'react';
import { Button } from '@gravity-ui/uikit';
import { PageHeader } from '../../../components';

export interface ForwardPageHeaderProps {
    onAddRule: () => void;
    onDeleteRules: () => void;
    isDeleteDisabled: boolean;
}

export const ForwardPageHeader: React.FC<ForwardPageHeaderProps> = ({
    onAddRule,
    onDeleteRules,
    isDeleteDisabled,
}) => (
    <PageHeader
        title="Forward"
        actions={
            <>
                <Button view="action" onClick={onAddRule}>
                    Add Rule
                </Button>
                <Button
                    view="outlined-danger"
                    onClick={onDeleteRules}
                    disabled={isDeleteDisabled}
                >
                    Delete Rules
                </Button>
            </>
        }
    />
);
