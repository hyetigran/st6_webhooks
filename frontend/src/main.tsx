import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { ApiError } from "./api/client.ts";
import { App } from "./App.tsx";
import "./index.css";
import { AuthProvider } from "./lib/auth.tsx";
import { BackendProvider } from "./lib/backend.tsx";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // ADR-008: polling every 2-5s, not push. A short default staleTime
      // avoids an extra fetch-on-mount burst on top of each view's own
      // refetchInterval.
      staleTime: 2000,
      // A 4xx (wrong API key, not found, bad request) will never succeed
      // on retry — without this, a persistently-wrong key combined with
      // any view's refetchInterval means the query keeps re-entering its
      // retry backoff and can sit in pending (neither loading nor error)
      // indefinitely, so the UI never shows the actual error.
      retry: (failureCount, error) => {
        if (error instanceof ApiError && error.status >= 400 && error.status < 500) return false;
        return failureCount < 2;
      },
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BackendProvider>
        <AuthProvider>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </AuthProvider>
      </BackendProvider>
    </QueryClientProvider>
  </StrictMode>,
);
