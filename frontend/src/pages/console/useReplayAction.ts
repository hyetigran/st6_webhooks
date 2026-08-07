import { useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useState } from "react";
import { useApiClient } from "../../api/useApiClient";
import type { AsyncAcceptedResponse } from "../../api/types";

/** Shared by the endpoints list and the single-endpoint queue view — both
 * offer a "Replay…" action that opens the same range-picker form. */
export function useReplayAction(queryKeys: QueryKey[]) {
  const client = useApiClient();
  const queryClient = useQueryClient();

  const [replayTarget, setReplayTarget] = useState<{ id: string; url: string } | null>(null);
  const [replayResult, setReplayResult] = useState<AsyncAcceptedResponse | null>(null);

  const replayMutation = useMutation({
    mutationFn: (input: { endpointId: string; range_start: string; range_end: string }) =>
      client!.triggerReplay(
        input.endpointId,
        { range_start: input.range_start, range_end: input.range_end },
        crypto.randomUUID(),
      ),
    onSuccess: (result) => {
      setReplayTarget(null);
      setReplayResult(result);
      queryKeys.forEach((queryKey) => queryClient.invalidateQueries({ queryKey }));
    },
  });

  return { replayTarget, setReplayTarget, replayResult, setReplayResult, replayMutation };
}
