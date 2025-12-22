// Prefix item for table display
export interface PrefixItem {
    id: string;
    prefix: string;
}

// Props for AddPrefixDialog
export interface AddPrefixDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (configName: string, prefixes: string[]) => Promise<void>;
    existingConfigs: string[];
}

// Props for DeletePrefixDialog
export interface DeletePrefixDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => Promise<void>;
    selectedPrefixes: string[];
}
