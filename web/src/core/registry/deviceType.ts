import type { ComponentType } from 'react';
import type { DeviceId, DevicePipeline } from '../api/devices';

/** A device managed by the Devices page, independent of its concrete type.
 *
 * Type-specific editable state lives in `ext`, keyed by device type and owned
 * by that type's plugin; the core treats it as opaque.
 */
export interface BaseDevice {
    id: DeviceId;
    type: string;
    inputPipelines: DevicePipeline[];
    outputPipelines: DevicePipeline[];
    isNew: boolean;
    isDirty: boolean;

    /** True once the type-specific ext slice has been hydrated from the server.
     *
     * Types without a `loadData` hook are considered loaded immediately.
     */
    loaded: boolean;

    /** Per-type editable state keyed by device type; each plugin owns its slice. */
    ext: Record<string, unknown>;
}

/** Icon component contributed by a device type, matching the shared icon shape. */
export type DeviceIcon = ComponentType<{ size?: number; color?: string }>;

/** One row in the device Properties grid. */
export interface DevicePropertyRow {
    label: string;
    value: string;
    mono?: boolean;
}

/** Props passed to a device type's detail-panel extension component.
 *
 * `ext` is this device's slice for the type; `onUpdateExt` merges a patch into
 * it without the component needing to know the surrounding device shape.
 */
export interface DeviceDetailProps {
    device: BaseDevice;
    ext: unknown;
    onUpdateExt: (patch: Record<string, unknown>) => void;
}

/** How a device type is laid out under the "by parent" grouping.
 *
 * `instances` emits one group per device (e.g. physical NICs); `group` collects
 * every device of the type into a single group.
 */
export type DeviceParentMode = 'group' | 'instances';

/** Everything the Devices page needs to render and manage one device type.
 *
 * A device type registers by exporting one of these as `deviceType` from its
 * `devices/<name>/web/device.ts`. The core discovers it at build time, so a new
 * type plugs into the shared Devices page without any edits to the core.
 */
export interface DeviceTypeManifest {
    type: string;
    label: string;
    pluralLabel: string;
    navOrder: number;
    icon: DeviceIcon;
    accentColor: string;

    /** Tag shown next to the device name, e.g. "PHYSICAL" or "VLAN · 10". */
    kindTag: (device: BaseDevice) => string;

    /** Human description for the Properties "Type" row. */
    typeDescription: string;

    /** One-line subtitle for a list row. */
    rowSubtitle?: (device: BaseDevice) => string;

    /** Small inline badge for a list row, e.g. the vlan id. */
    rowBadge?: (device: BaseDevice) => string | undefined;

    /** Grouping behaviour under the "by parent" view; defaults to `group`. */
    parentMode?: DeviceParentMode;

    /** Group label under the "by parent" view; defaults to `pluralLabel`. */
    parentGroupLabel?: string;

    /** Extra Properties rows specific to this type. */
    propertyRows?: (device: BaseDevice) => DevicePropertyRow[];

    /** Detail-panel section rendered below the shared sections, loaded lazily. */
    loadDetail?: () => Promise<{ default: ComponentType<DeviceDetailProps> }>;

    /** Seed the ext slice when a device is created locally. */
    createDefaults?: () => Record<string, unknown>;

    /** Fetch the server-side ext slice the first time the device is opened. */
    loadData?: (device: BaseDevice) => Promise<Record<string, unknown>>;

    /** Persist the device; returns the server-confirmed ext slice. */
    save: (
        device: BaseDevice,
        snapshot: BaseDevice | undefined,
    ) => Promise<Record<string, unknown>>;

    /** Whether the ext slice differs from the clean snapshot. */
    extDirty?: (device: BaseDevice, snapshot: BaseDevice | undefined) => boolean;

    /** When false, Save commits directly instead of opening the YAML diff modal. */
    confirmViaDiff?: boolean;

    /** Extra keys merged into the YAML diff for this type, e.g. vlan_id. */
    diffYaml?: (device: BaseDevice) => Record<string, unknown>;
}
