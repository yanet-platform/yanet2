export * from './common';
export * from './routes';
export * from './neighbours';
export * from './inspect';
export * from './functions';
export * from './pipelines';
export * from './devices';

import { neighbours } from './neighbours';
import { inspect } from './inspect';
import { route } from './routes';
import { functions } from './functions';
import { pipelines } from './pipelines';
import { devices } from './devices';

export const API = {
    neighbours,
    inspect,
    route,
    functions,
    pipelines,
    devices,
};
