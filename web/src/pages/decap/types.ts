import type { InstanceConfig } from '../../api/decap';

// Prefix item for table display
export interface PrefixItem {
    id: string;
    prefix: string;
}

// Decap instance data with configs and their prefixes
export interface DecapInstanceData {
    instance: number;
    configs: string[];
    configPrefixes: Map<string, string[]>;
}

// Props for PrefixTable
export interface PrefixTableProps {
    prefixes: PrefixItem[];
    selectedIds: Set<string>;
    onSelectionChange: (ids: Set<string>) => void;
    onAddPrefix: () => void;
}

// Props for AddPrefixDialog
export interface AddPrefixDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (prefixes: string[]) => Promise<void>;
}

// Props for DeletePrefixDialog
export interface DeletePrefixDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => Promise<void>;
    selectedPrefixes: string[];
}

export type { InstanceConfig };
