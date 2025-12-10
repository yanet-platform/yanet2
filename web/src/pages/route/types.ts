import type { TableColumnConfig } from '@gravity-ui/uikit';
import type { Route } from '../../api/routes';

// Form data types
export interface AddRouteFormData {
    configName: string;
    prefix: string;
    nexthopAddr: string;
    doFlush: boolean;
}

// Route table common props
export interface RouteTableBaseProps {
    routes: Route[];
    columns: TableColumnConfig<Route>[];
    selectedIds: string[];
    onSelectionChange: (ids: string[]) => void;
    getRowId: (route: Route) => string;
}

// Virtualized table uses the same base but with generator
export type RouteTableProps = RouteTableBaseProps;

// Header props
export interface RoutePageHeaderProps {
    onAddRoute: () => void;
    onDeleteRoute: () => void;
    onFlush: () => void;
    isDeleteDisabled: boolean;
    isFlushDisabled: boolean;
    mockEnabled?: boolean;
    onMockToggle?: (enabled: boolean) => void;
    mockSize?: string;
    onMockSizeChange?: (size: string) => void;
}

// List item props
export interface RouteListItemProps {
    route: Route;
}

// Dialog props
export interface DeleteRouteDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => void;
    selectedRoutes: Route[];
}

export interface AddRouteDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: () => void;
    form: AddRouteFormData;
    onFormChange: (form: AddRouteFormData) => void;
    validatePrefix: (prefix: string) => string | undefined;
    validateNexthop: (nexthop: string) => string | undefined;
}

// Config routes data structure - shared across components
export interface ConfigRoutesData {
    routes: Route[];
    selectedIds: string[];
}

// Common props for components that display routes by config
export interface RoutesByConfigProps {
    configs: string[];
    activeConfig: string;
    onConfigChange: (config: string) => void;
    getRoutesData: (configName: string) => ConfigRoutesData;
    onSelectionChange: (configName: string, ids: string[]) => void;
    getRouteId: (route: Route) => string;
}

// Props for ConfigTabs and InstanceTabContent (without routeColumns since VirtualizedRouteTable doesn't need it)
export interface ConfigTabsProps extends RoutesByConfigProps { }
export interface InstanceTabContentProps extends RoutesByConfigProps { }

// Sorting types
export type SortableColumn = 'prefix' | 'nextHop' | 'peer' | 'isBest' | 'pref' | 'asPathLen' | 'source';
export type SortDirection = 'asc' | 'desc';

export interface SortState {
    column: SortableColumn | null;
    direction: SortDirection;
}
