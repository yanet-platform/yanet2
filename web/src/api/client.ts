const callGRPCServiceWithBody = async <T>(
    servicePath: string,
    body: any,
    signal?: AbortSignal
): Promise<T> => {
    const response = await fetch(`/api/${servicePath}`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
        },
        body: JSON.stringify(body),
        signal,
    });

    if (!response.ok) {
        throw new Error(`HTTP error: ${response.status} ${response.statusText}`);
    }

    return await response.json() as T;
};

const callGRPCService = async <T>(
    servicePath: string,
    signal?: AbortSignal
): Promise<T> => {
    return callGRPCServiceWithBody<T>(servicePath, {}, signal);
};

export const createService = (serviceName: string) => {
    return {
        call: <T>(method: string, signal?: AbortSignal): Promise<T> => {
            return callGRPCService<T>(`${serviceName}/${method}`, signal);
        },
        callWithBody: <T>(method: string, body: any, signal?: AbortSignal): Promise<T> => {
            return callGRPCServiceWithBody<T>(`${serviceName}/${method}`, body, signal);
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
        const response = await fetch(`/api/${servicePath}`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify(body),
            signal,
        });

        if (!response.ok) {
            throw new Error(`HTTP error: ${response.status} ${response.statusText}`);
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
