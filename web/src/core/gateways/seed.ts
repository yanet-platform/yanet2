import type { Gateway } from './types';
import { builtinFromConfig, seedGatewaysFromConfig, extraGatewaysFromConfig } from './runtimeConfig';
import type { RuntimeConfig } from './runtimeConfig';

const EMPTY_CONFIG: RuntimeConfig = { defaultBackendUrl: '', gateways: [] };

/**
 * Default gateway inventory seeded on first load when no `/config.json` is
 * present.
 *
 * The builtin localhost entry is always first and carries an empty baseUrl
 * so same-origin API calls work out of the box against the dev server or
 * production host. It is non-deletable and non-editable.
 */
export const SEED_GATEWAYS: Gateway[] = seedGatewaysFromConfig(EMPTY_CONFIG);

/** The builtin localhost entry that is always present. */
export const BUILTIN_GATEWAY: Gateway = builtinFromConfig(EMPTY_CONFIG);

export const SEED_ACTIVE_ID = 'localhost';

export { builtinFromConfig, seedGatewaysFromConfig, extraGatewaysFromConfig };
export type { RuntimeConfig, ConfigGateway } from './runtimeConfig';
