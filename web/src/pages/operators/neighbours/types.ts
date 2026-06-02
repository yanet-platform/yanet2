export type SortableColumn =
    | 'next_hop'
    | 'link_addr'
    | 'hardware_addr'
    | 'device'
    | 'state'
    | 'source'
    | 'priority'
    | 'updated_at';

export type SortDirection = 'asc' | 'desc';

export interface SortState {
    column: SortableColumn | null;
    direction: SortDirection;
}

export const DEFAULT_SORT: SortState = { column: 'state', direction: 'asc' };

export const MERGED_TAB = '__merged__';
