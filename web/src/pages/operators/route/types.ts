export type RouteSortableColumn = 'prefix' | 'next_hop' | 'peer' | 'is_best' | 'pref' | 'as_path_len' | 'source';
export type SortDirection = 'asc' | 'desc';
export type { IPFamily } from '@yanet/core/components/VirtualTable';

export interface RouteSortState {
    column: RouteSortableColumn | null;
    direction: SortDirection;
}
