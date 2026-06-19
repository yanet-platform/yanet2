import type { Gateway } from './types';

/**
 * Default gateway inventory seeded on first load.
 *
 * The builtin localhost entry is always first and carries an empty baseUrl
 * so same-origin API calls work out of the box against the dev server or
 * production host. It is non-deletable and non-editable.
 */
export const SEED_GATEWAYS: Gateway[] = [
    {
        id: 'localhost',
        host: 'localhost',
        numa: 0,
        addr: 'same-origin',
        baseUrl: '',
        status: 'online',
        builtin: true,
    }
];

/** The builtin localhost entry that is always present. */
export const BUILTIN_GATEWAY: Gateway = SEED_GATEWAYS[0];

export const SEED_ACTIVE_ID = 'localhost';
