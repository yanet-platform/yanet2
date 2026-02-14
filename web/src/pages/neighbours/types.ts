import type { Neighbour, NeighbourTableInfo } from '../../api/neighbours';

export interface AddNeighbourDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (table: string, entry: Neighbour) => Promise<void>;
    tables: NeighbourTableInfo[];
    defaultTable: string;
}

export interface EditNeighbourDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (table: string, entry: Neighbour) => Promise<void>;
    neighbour: Neighbour | null;
    table: string;
}

export interface RemoveNeighboursDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => Promise<void>;
    selectedCount: number;
}

export interface CreateTableDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (name: string, defaultPriority: number) => Promise<void>;
}

export interface EditTableDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (name: string, defaultPriority: number) => Promise<void>;
    tableInfo: NeighbourTableInfo | null;
}

export interface RemoveTableDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => Promise<void>;
    tableName: string;
}
