import type { Rule } from '../../api/forward';

// Rule item for table display with index
export interface RuleItem {
    id: string; // Unique id based on index
    index: number;
    rule: Rule;
}

// Props for AddRuleDialog
export interface AddRuleDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (configName: string, rule: Rule) => Promise<void>;
    existingConfigs: string[];
    currentConfig?: string;
}

// Props for EditRuleDialog
export interface EditRuleDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (rule: Rule) => Promise<void>;
    rule: Rule | null;
    ruleIndex: number;
}

// Props for DeleteRuleDialog
export interface DeleteRuleDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => Promise<void>;
    selectedCount: number;
}

// Props for RuleTable
export interface RuleTableProps {
    rules: RuleItem[];
    selectedIds: Set<string>;
    onSelectionChange: (ids: Set<string>) => void;
    onEditRule: (ruleItem: RuleItem) => void;
}

// Props for ConfigTabs
export interface ConfigTabsProps {
    configs: string[];
    activeConfig: string;
    onConfigChange: (config: string) => void;
    renderContent: (configName: string) => React.ReactNode;
}
