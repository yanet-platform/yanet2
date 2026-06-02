export type RouteSortableColumn = 'prefix' | 'next_hop' | 'peer' | 'is_best' | 'pref' | 'as_path_len' | 'source';
export type SortDirection = 'asc' | 'desc';
export type IPFamily = 'all' | 'v4' | 'v6';

export interface RouteSortState {
    column: RouteSortableColumn | null;
    direction: SortDirection;
}
