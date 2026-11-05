// A browser fetch that gets no HTTP response (offline, DNS, connection reset)
// rejects with a TypeError such as "Failed to fetch". Every console request
// goes through consoleFetch so that case reads the same everywhere; a response
// with an HTTP status, even an error status, is returned unchanged.

export const NETWORK_FAILURE_MESSAGE = "网络连接中断，无法连接控制台服务，请检查网络后重试"

// Extends TypeError so callers that already treat a fetch TypeError as "the
// request may or may not have arrived" keep doing so.
export class NetworkError extends TypeError {
  constructor() {
    super(NETWORK_FAILURE_MESSAGE)
    this.name = "NetworkError"
  }
}

export function isNetworkError(err: unknown): err is NetworkError {
  return err instanceof NetworkError
}

function aborted(err: unknown, init?: RequestInit): boolean {
  return Boolean(init?.signal?.aborted) || (err instanceof Error && err.name === "AbortError")
}

// subscribeNetworkStatus reports the browser's offline/online events, so the
// console can show a banner while offline. It never retries anything.
export function subscribeNetworkStatus(
  events: Pick<EventTarget, "addEventListener" | "removeEventListener">,
  onChange: (online: boolean) => void,
): () => void {
  const offline = () => onChange(false)
  const online = () => onChange(true)
  events.addEventListener("offline", offline)
  events.addEventListener("online", online)
  return () => {
    events.removeEventListener("offline", offline)
    events.removeEventListener("online", online)
  }
}

export async function consoleFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  try {
    return await globalThis.fetch(input, init)
  } catch (err) {
    // A cancelled request is the caller's own choice, not a network failure.
    if (aborted(err, init)) throw err
    if (err instanceof TypeError) throw new NetworkError()
    throw err
  }
}
