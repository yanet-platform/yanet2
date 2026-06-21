/** Status of a gateway control-plane endpoint. */
export type GatewayStatus = 'online' | 'degraded' | 'offline' | 'checking';

/** A single control-plane endpoint, scoped to one NUMA node. */
export interface Gateway {
    id: string;
    host: string;
    numa: number;
    /** API address shown in the UI, e.g. "10.20.0.11:8080". */
    addr: string;
    /**
     * Absolute origin used to re-point API fetch calls.
     *
     * An empty string means same-origin (the current dev server or production host).
     */
    baseUrl: string;
    status: GatewayStatus;
    /**
     * Whether this is a built-in, non-deletable system entry.
     *
     * Built-in gateways (e.g. the localhost same-origin entry) are always present,
     * cannot be edited or deleted, and survive localStorage upgrades.
     */
    builtin?: boolean;
}
