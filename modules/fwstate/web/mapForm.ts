// Pure validation and sizing helpers for the fwstate-map create/insert-layer
// forms. Extracted from MapsPanel so the worker_count / uint clamping rules
// can be unit-tested without rendering.

// Highest worker_count accepted by the server (uint16 range), matching the
// C-side validateWorkerCount check.
export const MAX_WORKER_COUNT = 65535;

// Default form values; mirror the inline map_config defaults used elsewhere in
// the fwstate page (1M index slots, 1024 overflow buckets, single worker).
export const DEFAULT_INDEX_SIZE = 1_048_576;
export const DEFAULT_EXTRA_BUCKET_COUNT = 1_024;
export const DEFAULT_WORKER_COUNT = 1;

export interface MapSizingFields {
    indexSize: string;
    extraBucketCount: string;
    workerCount: string;
}

export const EMPTY_SIZING: MapSizingFields = {
    indexSize: String(DEFAULT_INDEX_SIZE),
    extraBucketCount: String(DEFAULT_EXTRA_BUCKET_COUNT),
    workerCount: String(DEFAULT_WORKER_COUNT),
};

// Largest value that fits in a uint32 field. Used to clamp form input before
// sending it to the server.
const MAX_UINT32 = 4_294_967_295;

/** Clamp a free-form text field to a non-negative uint32-safe integer. */
export const toUint = (value: string): number => {
    const parsed = Number(value);
    if (!Number.isFinite(parsed) || !Number.isInteger(parsed) || parsed < 0) return 0;
    if (parsed > MAX_UINT32) return MAX_UINT32;
    return parsed;
};

/**
 * Validate the worker_count form field.
 *
 * Returns an error message when the value is missing, non-integer, below 1, or
 * above the uint16 ceiling enforced server-side. Returns undefined when valid.
 */
export const validateWorkerCount = (value: string): string | undefined => {
    const parsed = Number(value);
    if (!Number.isFinite(parsed) || !Number.isInteger(parsed) || parsed < 1) {
        return 'Worker count must be an integer ≥ 1';
    }
    if (parsed > MAX_WORKER_COUNT) {
        return `Worker count must not exceed ${MAX_WORKER_COUNT}`;
    }
    return undefined;
};
