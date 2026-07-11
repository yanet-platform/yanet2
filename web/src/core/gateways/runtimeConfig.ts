import { deriveBaseUrl } from './deriveBaseUrl';
import type { Gateway } from './types';

/** A gateway entry in the runtime config file. */
export interface ConfigGateway {
    host: string;
    numa: number;
    addr: string;
}

/** Shape of the `/config.json` file served alongside the SPA. */
export interface RuntimeConfig {
    /**
     * Base URL of the default (builtin) backend.
     *
     * An empty string means same-origin — the SPA is served by the gateway
     * itself and API calls go to `/api/...` on the current origin.
     *
     * Set to a full URL (e.g. `http://10.0.0.10:8081`) when the SPA is
     * deployed separately from the gateway.
     */
    defaultBackendUrl: string;
    /** Additional gateways to seed so users can switch between them. */
    gateways: ConfigGateway[];
}

const EMPTY_CONFIG: RuntimeConfig = {
    defaultBackendUrl: '',
    gateways: [],
};

const isRecord = (v: unknown): v is Record<string, unknown> =>
    typeof v === 'object' && v !== null;

const parseConfig = (raw: unknown): RuntimeConfig => {
    if (!isRecord(raw)) {
        return EMPTY_CONFIG;
    }
    const defaultBackendUrl =
        typeof raw.defaultBackendUrl === 'string' ? raw.defaultBackendUrl.trim() : '';
    const gateways = Array.isArray(raw.gateways)
        ? raw.gateways
              .filter(isRecord)
              .map((g) => ({
                  host: typeof g.host === 'string' ? g.host : 'unnamed',
                  numa: typeof g.numa === 'number' ? g.numa : 0,
                  addr: typeof g.addr === 'string' ? g.addr : '',
              }))
              .filter((g) => g.addr.length > 0)
        : [];
    return { defaultBackendUrl, gateways };
};

/**
 * Fetches `/config.json` and returns the parsed runtime configuration.
 *
 * The file is optional — if it does not exist (404) or fails to parse, an
 * empty config is returned and the SPA falls back to same-origin defaults.
 */
export const loadRuntimeConfig = async (): Promise<RuntimeConfig> => {
    try {
        const res = await fetch('/config.json', { cache: 'no-cache' });
        if (!res.ok) {
            return EMPTY_CONFIG;
        }
        return parseConfig(await res.json());
    } catch {
        return EMPTY_CONFIG;
    }
};

/** Builds the builtin gateway from a runtime config. */
export const builtinFromConfig = (config: RuntimeConfig): Gateway => {
    const url = config.defaultBackendUrl;
    if (!url) {
        return {
            id: 'localhost',
            host: 'localhost',
            numa: 0,
            addr: 'same-origin',
            baseUrl: '',
            status: 'online',
            builtin: true,
        };
    }
    let host = 'default';
    try {
        host = new URL(url).hostname || 'default';
    } catch {
        // keep 'default'
    }
    return {
        id: 'localhost',
        host,
        numa: 0,
        addr: url,
        baseUrl: url,
        status: 'online',
        builtin: true,
    };
};

/** Builds the full seed gateway list from a runtime config. */
export const seedGatewaysFromConfig = (config: RuntimeConfig): Gateway[] => {
    const builtin = builtinFromConfig(config);
    const extras: Gateway[] = config.gateways.map((g, i) => ({
        id: `seed-${i}-${g.host}-${g.numa}`,
        host: g.host,
        numa: g.numa,
        addr: g.addr,
        baseUrl: deriveBaseUrl(g.addr),
        status: 'online',
    }));
    return [builtin, ...extras];
};
