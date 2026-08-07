import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
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
      // No client-side retry: every query here already has a
      // refetchInterval, which is itself a natural retry — the next poll
      // tick tries again in a few seconds regardless. Layering TanStack
      // Query's own multi-attempt backoff on top of that is redundant and
      // was actively broken: a retry's backoff timer can still be
      // in-flight when the next refetchInterval tick fires, and that
      // interval-triggered fetch resets the retry sequence's own
      // failureCount back to 0 (confirmed by direct instrumentation — it
      // never advanced past 0 across repeated real failures), so the
      // retry limit was never reached and isError never became true: a
      // persistently-failing query (e.g. a genuine 500, not just a bad
      // API key) sat in pending forever, showing neither loading nor an
      // error. Cutting retry out entirely removes the interaction
      // instead of trying to out-race it — a failed fetch just shows its
      // error immediately, and self-heals on the next poll tick like any
      // other polling-based UI.
      retry: false,
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
