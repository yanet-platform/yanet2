import type { DeviceTypeManifest } from './deviceType';

const discovered = import.meta.glob<DeviceTypeManifest>(
    '../../../../devices/*/web/device.ts',
    { eager: true, import: 'deviceType' },
);

/** All registered device types, ordered for list grouping and filter chips. */
export const deviceTypes: DeviceTypeManifest[] = Object.values(discovered).sort(
    (a, b) => a.navOrder - b.navOrder,
);

const byType = new Map<string, DeviceTypeManifest>(
    deviceTypes.map((manifest) => [manifest.type, manifest]),
);

/** Look up the manifest for a device type, or undefined if none is registered. */
export const deviceTypeManifest = (
    type: string,
): DeviceTypeManifest | undefined => byType.get(type);
