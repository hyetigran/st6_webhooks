import { useMemo } from "react";
import { useAuth } from "../lib/auth";
import { useBackend } from "../lib/backend";
import { createApiClient } from "./client";

/** Rebuilds whenever the selected backend or the signed-in API key
 * changes — callers should fold `backend.id`/`apiKey` into their
 * TanStack Query keys too, so switching backends invalidates cached
 * data instead of showing the other backend's stale results. */
export function useApiClient() {
  const { backend } = useBackend();
  const { apiKey } = useAuth();

  return useMemo(() => {
    if (!apiKey) return null;
    return createApiClient({ baseUrl: backend.baseUrl, apiKey });
  }, [backend.baseUrl, apiKey]);
}
