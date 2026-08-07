import { useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useState } from "react";
import { useApiClient } from "../../api/useApiClient";
import type { ResumeEndpointResponse } from "../../api/types";

export type RevealedSecret = { url: string; secret: string; overlapExpiresAt?: string };

/** Shared pause/resume/rotate-secret mutations and their result/error UI
 * state — used by both the endpoints list (Endpoints.tsx) and the single-
 * endpoint queue view (EndpointDetail.tsx), so this logic exists once. */
export function useEndpointActions(queryKey: QueryKey) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const invalidate = () => queryClient.invalidateQueries({ queryKey });

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
    mutationFn: (vars: { id: string; url: string }) => client!.rotateSecret(vars.id),
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
