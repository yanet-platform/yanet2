import React from 'react';
import { Table, withTableSorting, withTableSelection } from '@gravity-ui/uikit';
import { EmptyState } from '../../components';
import type { RouteTableProps } from './types';

const SortableSelectableTable = withTableSelection(withTableSorting(Table));

export const RouteTable: React.FC<RouteTableProps> = ({
    routes,
    columns,
    selectedIds,
    onSelectionChange,
    getRowId,
}) => {
    if (routes.length === 0) {
        return <EmptyState message="No routes found" />;
    }

    return (
        <SortableSelectableTable
            data={routes}
            columns={columns}
            width="max"
            selectedIds={selectedIds}
            onSelectionChange={onSelectionChange}
            getRowId={getRowId}
        />
    );
};
