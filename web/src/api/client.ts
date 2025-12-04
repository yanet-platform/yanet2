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
