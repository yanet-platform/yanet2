export type ChipKind = 'cidr' | 'device' | 'vlan';

export interface ChipInputProps {
    value: string[];
    onChange: (values: string[]) => void;
    placeholder?: string;
    kind: ChipKind;
    wildcardLabel?: string;
    validator: (s: string) => boolean;
}

/** Imperative handle for synchronously flushing pending text before a parent save. */
export interface ChipInputHandle {
    /**
     * Synchronously return any tokens in the current draft text and clear it.
     * Does NOT call onChange — the caller merges the returned tokens itself so
     * that draft text and committed chips land in one synchronous transaction.
     */
    flush(): string[];
}
