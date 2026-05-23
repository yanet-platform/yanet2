import type { Neighbour, NeighbourTableInfo } from '../../../api/neighbours';

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

export type NeighbourDrawerMode = 'add' | 'edit';

export interface NeighbourDrawerProps {
    open: boolean;
    mode: NeighbourDrawerMode;
    tables: NeighbourTableInfo[];
    defaultTable: string;
    neighbour: Neighbour | null;
    activeTable: string;
    onClose: () => void;
    onSubmit: (table: string, entry: Neighbour) => Promise<void>;
    onDelete?: (neighbour: Neighbour) => Promise<void>;
}

export interface CreateTableModalProps {
    open: boolean;
    onClose: () => void;
    onCreate: (name: string, defaultPriority: number) => Promise<void>;
    existingNames: string[];
}

export interface EditTableModalProps {
    open: boolean;
    onClose: () => void;
    onSave: (name: string, defaultPriority: number) => Promise<void>;
    tableInfo: NeighbourTableInfo | null;
}
