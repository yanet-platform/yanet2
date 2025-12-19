import type { Rule, MapConfig, SyncConfig } from '../../api/acl';

// Config state types
export type ConfigState = 'saved' | 'modified' | 'new';

export interface AclConfigData {
    rules: Rule[];
    fwstateMap?: MapConfig;
    fwstateSync?: SyncConfig;
    state: ConfigState;
    originalRules?: Rule[]; // For detecting changes
    originalFwstateMap?: MapConfig;
    originalFwstateSync?: SyncConfig;
}

// Form data for YAML upload
export interface UploadYamlFormData {
    configName: string;
    file: File | null;
    parsedRules: Rule[] | null;
    parseError: string | null;
}

// YAML file structure (matches CLI format)
export interface YamlAclRule {
    srcs: string[];
    dsts: string[];
    src_ports: Array<{ from: number; to: number }>;
    dst_ports: Array<{ from: number; to: number }>;
    proto_ranges: Array<{ from: number; to: number }>;
    vlan_ranges: Array<{ from: number; to: number }>;
    devices: string[];
    counter: string;
    action: 'Allow' | 'Deny';
}

export interface YamlAclConfig {
    rules: YamlAclRule[];
}

// Page header props
export interface AclPageHeaderProps {
    onUploadYaml: () => void;
    onSave: () => void;
    onDeleteConfig: () => void;
    isSaveDisabled: boolean;
    isDeleteDisabled: boolean;
    hasUnsavedChanges: boolean;
    isSaving: boolean;
}

// Config tabs props
export interface ConfigTabsProps {
    configs: string[];
    activeConfig: string;
    configStates: Map<string, ConfigState>;
    onConfigChange: (config: string) => void;
    onTryChangeConfig: (config: string) => void; // Called when user tries to change config
}

// Inner tabs props
export interface InnerTabsProps {
    activeTab: 'rules' | 'fwstate';
    onTabChange: (tab: 'rules' | 'fwstate') => void;
}

// Table props
export interface AclTableProps {
    rules: Rule[];
    searchQuery: string;
    onSearchChange: (query: string) => void;
    isLoading?: boolean;
}

// FW State form props
export interface FWStateFormProps {
    mapConfig?: MapConfig;
    syncConfig?: SyncConfig;
    onMapConfigChange: (config: MapConfig) => void;
    onSyncConfigChange: (config: SyncConfig) => void;
    onSave: () => void;
    hasChanges: boolean;
}

// Dialog props
export interface UploadYamlDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (configName: string, rules: Rule[]) => void;
    existingConfigs: string[];
}

export interface CreateConfigDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (configName: string) => void;
    existingConfigs: string[];
}

export interface DeleteConfigDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => void;
    configName: string;
}

export interface UnsavedChangesDialogProps {
    open: boolean;
    onClose: () => void;
    onDiscard: () => void;
    onSave: () => void;
    configName: string;
}
