// Prefix item for table display
export interface PrefixItem {
    id: string;
    prefix: string;
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
