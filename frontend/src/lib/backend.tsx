import { createContext, useContext, useMemo, useState, type ReactNode } from "react";

export interface BackendOption {
  id: "node" | "go";
  label: string;
  baseUrl: string;
}

// Base URLs are env-configurable (ADR-008: "a configurable base URL")
// with the docker-compose/local-dev defaults as fallback.
const BACKENDS: BackendOption[] = [
  { id: "node", label: "Node / TS", baseUrl: import.meta.env.VITE_NODE_API_URL ?? "http://localhost:3000" },
  { id: "go", label: "Go", baseUrl: import.meta.env.VITE_GO_API_URL ?? "http://localhost:8090" },
];

const STORAGE_KEY = "gauntlet-relay:backend";

interface BackendContextValue {
  backend: BackendOption;
  backends: BackendOption[];
  setBackendId: (id: BackendOption["id"]) => void;
}

const BackendContext = createContext<BackendContextValue | null>(null);

function loadInitialBackendId(): BackendOption["id"] {
  const stored = localStorage.getItem(STORAGE_KEY);
  return stored === "go" ? "go" : "node";
}

export function BackendProvider({ children }: { children: ReactNode }) {
  const [backendId, setBackendIdState] = useState<BackendOption["id"]>(loadInitialBackendId);

  const setBackendId = (id: BackendOption["id"]) => {
    localStorage.setItem(STORAGE_KEY, id);
    setBackendIdState(id);
  };

  const value = useMemo<BackendContextValue>(() => {
    const backend = BACKENDS.find((b) => b.id === backendId) ?? BACKENDS[0]!;
    return { backend, backends: BACKENDS, setBackendId };
  }, [backendId]);

  return <BackendContext.Provider value={value}>{children}</BackendContext.Provider>;
}

export function useBackend(): BackendContextValue {
  const ctx = useContext(BackendContext);
  if (!ctx) throw new Error("useBackend must be used within a BackendProvider");
  return ctx;
}
