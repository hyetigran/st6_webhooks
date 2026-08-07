import { useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useState } from "react";
import { useApiClient } from "../../api/useApiClient";
import type { ResumeEndpointResponse } from "../../api/types";

export type RevealedSecret = { url: string; secret: string; overlapExpiresAt?: string };

/** id+url travel together at every call site that needs to both act on an
 * endpoint and label a resulting modal with its URL (rotate-secret,
 * replay) — named here instead of repeating the pair as an inline object
 * type at each site. */
export type EndpointRef = { id: string; url: string };

/** Shared pause/resume/rotate-secret mutations and their result/error UI
 * state — used by both the endpoints list (Endpoints.tsx) and the single-
 * endpoint queue view (EndpointDetail.tsx), so this logic exists once.
 * Takes every query an action can invalidate — EndpointDetail.tsx passes
 * both the endpoint's own query and its deliveries-queue query, since a
 * resume/pause changes what the queue table should show too, not just the
 * endpoint's status badge. */
export function useEndpointActions(queryKeys: QueryKey[]) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const invalidate = () => queryKeys.forEach((queryKey) => queryClient.invalidateQueries({ queryKey }));

  const [revealedSecret, setRevealedSecret] = useState<RevealedSecret | null>(null);
  const [resumeDisclosure, setResumeDisclosure] = useState<ResumeEndpointResponse | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const onActionError = (err: unknown) => setActionError(err instanceof Error ? err.message : "Action failed");

  const pauseMutation = useMutation({
    mutationFn: (id: string) => client!.pauseEndpoint(id),
    onSuccess: invalidate,
    onError: onActionError,
  });

  const resumeMutation = useMutation({
    mutationFn: (id: string) => client!.resumeEndpoint(id),
    onSuccess: (result: ResumeEndpointResponse) => {
      setResumeDisclosure(result);
      invalidate();
    },
    onError: onActionError,
  });

  const rotateMutation = useMutation({
    mutationFn: (vars: EndpointRef) => client!.rotateSecret(vars.id),
    onSuccess: (result, vars) => {
      setRevealedSecret({ url: vars.url, secret: result.signing_secret, overlapExpiresAt: result.overlap_expires_at });
      invalidate();
    },
    onError: onActionError,
  });

  const busyEndpointId =
    (pauseMutation.isPending && pauseMutation.variables) ||
    (resumeMutation.isPending && resumeMutation.variables) ||
    (rotateMutation.isPending && rotateMutation.variables?.id) ||
    null;

  return {
    pauseMutation,
    resumeMutation,
    rotateMutation,
    busyEndpointId,
    revealedSecret,
    setRevealedSecret,
    resumeDisclosure,
    setResumeDisclosure,
    actionError,
    setActionError,
  };
}
