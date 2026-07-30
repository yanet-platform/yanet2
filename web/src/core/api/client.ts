export interface CallOptions {
    signal?: AbortSignal;
    compress?: boolean;
}

/** Error thrown for a non-2xx HTTP response from a gRPC-over-JSON call.
 *
 * Carries the numeric status alongside the formatted message so callers can
 * branch on it (e.g. treating a 404 as a missing config rather than a real
 * failure) instead of parsing the message text.
 */
export class ApiError extends Error {
    readonly status: number;
    readonly statusText: string;
    readonly detail: string;

    constructor(status: number, statusText: string, detail: string) {
        const base = `HTTP ${status} ${statusText}`;
        super(detail ? `${base}: ${detail}` : base);
        this.name = 'ApiError';
        this.status = status;
        this.statusText = statusText;
        this.detail = detail;
    }
}

let apiBase = '';

/** Set the base URL prefix for all API calls. An empty string means same-origin. */
export const setApiBase = (base: string): void => {
    apiBase = base;
};

const compressGzip = async (data: string): Promise<Blob> => {
    const stream = new Blob([data]).stream();
    const compressedStream = stream.pipeThrough(new CompressionStream('gzip'));
    return new Response(compressedStream).blob();
};

/** Extract a human-readable detail from an error response body. */
const readErrorDetail = async (response: Response): Promise<string> => {
    try {
        const text = await response.text();
        if (!text) return '';
        try {
            const parsed = JSON.parse(text);
            if (typeof parsed === 'object' && parsed !== null) {
                if (typeof parsed.message === 'string') return parsed.message;
                if (typeof parsed.error === 'string') return parsed.error;
            }
            return text;
        } catch {
            return text;
        }
    } catch {
        return '';
    }
};

const callGRPCServiceWithBody = async <T>(
    servicePath: string,
    body: any,
    options?: CallOptions
): Promise<T> => {
    const jsonBody = JSON.stringify(body);

    const headers: Record<string, string> = {
        'Content-Type': 'application/json',
    };

    let requestBody: string | Blob = jsonBody;

    // Only compress for same-origin requests; cross-origin gateways don't allow
    // Content-Encoding in CORS preflight, so skip compression when apiBase is set.
    if (options?.compress && !apiBase) {
        requestBody = await compressGzip(jsonBody);
        headers['Content-Encoding'] = 'gzip';
    }

    const response = await fetch(`${apiBase}/api/${servicePath}`, {
        method: 'POST',
        headers,
        body: requestBody,
        signal: options?.signal,
    });

    if (!response.ok) {
        const detail = await readErrorDetail(response);
        throw new ApiError(response.status, response.statusText, detail);
    }

    return await response.json() as T;
};

const callGRPCService = async <T>(
    servicePath: string,
    options?: CallOptions
): Promise<T> => {
    return callGRPCServiceWithBody<T>(servicePath, {}, options);
};

export interface LoadKnownConfigsOptions {
    /** Called when the name list was non-empty and every config was dropped. */
    onAllDropped?: (count: number) => void;
}

/** Marker substituted for a per-name load whose 404 means the config is gone. */
const DROPPED = Symbol('dropped');

/** Load one config per name concurrently, dropping names the backend does not know.
 *
 * The name list and the per-name loader can come from two different
 * registries: the dataplane's shared-memory inventory survives a control-plane
 * restart, while a module service's in-process map is rebuilt empty. Where
 * both come from the same service, a name can still be deleted between the two
 * calls. A 404 therefore means the backend has no such config and it is
 * skipped. Any other failure, including a rejection that is not an ApiError,
 * rejects the whole load as soon as it happens, so a hung or slow sibling
 * request never delays surfacing a real error. The rejection is rethrown
 * unchanged so the caller still sees the original error.
 *
 * When every name was dropped and at least one name was requested, every
 * request was a 404, since any other rejection would already have rejected
 * the whole load above. `onAllDropped` fires in exactly that case, so a
 * caller can warn that none of the requested configs came back instead of
 * rendering a silent empty state.
 */
export const loadKnownConfigs = async <T>(
    names: string[],
    loadOne: (name: string) => Promise<T>,
    options?: LoadKnownConfigsOptions,
): Promise<T[]> => {
    const results: (T | typeof DROPPED)[] = await Promise.all(names.map((name): Promise<T | typeof DROPPED> =>
        loadOne(name).catch((reason: unknown) => {
            if (reason instanceof ApiError && reason.status === 404) {
                return DROPPED;
            }
            throw reason;
        })
    ));

    const values = results.filter((result): result is T => result !== DROPPED);
    if (names.length > 0 && values.length === 0) {
        options?.onAllDropped?.(names.length);
    }
    return values;
};

export const createService = (serviceName: string) => {
    return {
        call: <T>(method: string, options?: CallOptions): Promise<T> => {
            return callGRPCService<T>(`${serviceName}/${method}`, options);
        },
        callWithBody: <T>(method: string, body: any, options?: CallOptions): Promise<T> => {
            return callGRPCServiceWithBody<T>(`${serviceName}/${method}`, body, options);
        },
    };
};

// SSE streaming types and utilities

export interface StreamCallbacks<T> {
    onMessage: (data: T) => void;
    onError?: (error: Error) => void;
    onEnd?: () => void;
}

interface SSEEvent {
    event: string;
    data: string;
}

// Parse SSE events from a chunk of text
const parseSSEEvents = (text: string): SSEEvent[] => {
    const events: SSEEvent[] = [];
    const blocks = text.split('\n\n');

    for (const block of blocks) {
        if (!block.trim()) continue;

        const lines = block.split('\n');
        let event = 'message';
        let data = '';

        for (const line of lines) {
            if (line.startsWith('event: ')) {
                event = line.slice(7);
            } else if (line.startsWith('data: ')) {
                data = line.slice(6);
            }
        }

        if (data) {
            events.push({ event, data });
        }
    }

    return events;
};

// Stream gRPC service call with SSE response
const streamGRPCService = async <T>(
    servicePath: string,
    body: any,
    callbacks: StreamCallbacks<T>,
    signal?: AbortSignal
): Promise<void> => {
    try {
        const response = await fetch(`${apiBase}/api/${servicePath}`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify(body),
            signal,
        });

        if (!response.ok) {
            const detail = await readErrorDetail(response);
            throw new ApiError(response.status, response.statusText, detail);
        }

        if (!response.body) {
            throw new Error('Response body is null');
        }

        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        while (true) {
            const { done, value } = await reader.read();

            if (done) {
                // Process any remaining buffer
                if (buffer.trim()) {
                    const events = parseSSEEvents(buffer);
                    for (const evt of events) {
                        processSSEEvent(evt, callbacks);
                    }
                }
                callbacks.onEnd?.();
                break;
            }

            buffer += decoder.decode(value, { stream: true });

            // Process complete events (separated by \n\n)
            const lastDoubleNewline = buffer.lastIndexOf('\n\n');
            if (lastDoubleNewline !== -1) {
                const completeData = buffer.slice(0, lastDoubleNewline + 2);
                buffer = buffer.slice(lastDoubleNewline + 2);

                const events = parseSSEEvents(completeData);
                for (const evt of events) {
                    processSSEEvent(evt, callbacks);
                }
            }
        }
    } catch (error) {
        if (signal?.aborted) {
            // Stream was intentionally aborted
            callbacks.onEnd?.();
            return;
        }
        callbacks.onError?.(error instanceof Error ? error : new Error(String(error)));
    }
};

const processSSEEvent = <T>(evt: SSEEvent, callbacks: StreamCallbacks<T>): void => {
    switch (evt.event) {
        case 'message':
            try {
                const data = JSON.parse(evt.data) as T;
                callbacks.onMessage(data);
            } catch (parseError) {
                callbacks.onError?.(new Error(`Failed to parse message: ${evt.data}`));
            }
            break;
        case 'error':
            try {
                const errorData = JSON.parse(evt.data) as { code: number; message: string };
                callbacks.onError?.(new Error(`gRPC error ${errorData.code}: ${errorData.message}`));
            } catch {
                callbacks.onError?.(new Error(`Stream error: ${evt.data}`));
            }
            break;
        case 'end':
            callbacks.onEnd?.();
            break;
    }
};

export const createStreamingService = (serviceName: string) => {
    return {
        stream: <T>(
            method: string,
            body: any,
            callbacks: StreamCallbacks<T>,
            signal?: AbortSignal
        ): void => {
            streamGRPCService<T>(`${serviceName}/${method}`, body, callbacks, signal);
        },
    };
};
