import { Table, withTableSorting } from '@gravity-ui/uikit';
import type { TableColumnConfig, TableProps, TableSortState } from '@gravity-ui/uikit';

/**
 * Table component with sorting capabilities
 */
export const SortableDataTable = withTableSorting(Table);

export type { TableColumnConfig, TableProps, TableSortState };

