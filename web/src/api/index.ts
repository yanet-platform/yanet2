export { type CallOptions } from './client';
export * from './common';
export * from './routes';
export * from './neighbours';
export * from './inspect';
export * from './functions';
export * from './pipelines';
export * from './devices';
export * from './decap';
export * from './acl';
export * from './forward';

import { neighbours } from './neighbours';
import { inspect } from './inspect';
import { route } from './routes';
import { functions } from './functions';
import { pipelines } from './pipelines';
import { devices } from './devices';
import { decap } from './decap';
import { acl } from './acl';
import { forward } from './forward';

export const API = {
    neighbours,
    inspect,
    route,
    functions,
    pipelines,
    devices,
    decap,
    acl,
    forward,
};
