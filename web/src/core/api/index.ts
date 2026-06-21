export { type CallOptions, setApiBase } from './client';
export * from './common';
export * from './routes';
export * from './neighbours';
export * from './inspect';
export * from './functions';
export * from './pipelines';
export * from './devices';
export * from './decap';
export { acl, ACTION_KIND_LABELS } from './acl';
export * from './forward';
export { fwstate } from './fwstate';
export * from './counters';

// Several module clients each declare their own ShowConfigRequest /
// ShowConfigResponse / Device; re-export one explicitly so the barrel is
// unambiguous. Consumers that need a specific module's shape import it from
// that module's subpath rather than this barrel.
export type { ShowConfigRequest, ShowConfigResponse } from './decap';
export type { Device } from './devices';

import { neighbours } from './neighbours';
import { inspect } from './inspect';
import { route, routeOperator } from './routes';
import { functions } from './functions';
import { pipelines } from './pipelines';
import { devices } from './devices';
import { decap } from './decap';
import { acl } from './acl';
import { forward } from './forward';
import { fwstate } from './fwstate';
import { counters } from './counters';

export const API = {
    neighbours,
    inspect,
    route,
    routeOperator,
    functions,
    pipelines,
    devices,
    decap,
    acl,
    forward,
    fwstate,
    counters,
};
