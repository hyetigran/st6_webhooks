import type { IncomingMessage, ServerResponse } from "node:http";

export interface ConformingReceiver {
  handler: (req: IncomingMessage, res: ServerResponse) => void;
  processedEventIds: Set<string>;
  attemptsByEventId: Map<string, number>;
}

// REVIEW.md F-13 / PRD §6: receivers must dedupe on *successfully
// processed* event_id, not on event_id merely seen at attempt time — a
// receiver that marks an id "seen" before processing succeeds would
// silently no-op the replay of an event that never actually completed,
// defeating the reason to replay it. This is the reference fixture for
// that rule: `processedEventIds` only ever gains an entry on the attempt
// where `shouldSucceed` says the receiver's business logic actually
// completed, never on mere receipt.
export function createConformingReceiver(shouldSucceed: (eventId: string, attemptNumber: number) => boolean): ConformingReceiver {
  const processedEventIds = new Set<string>();
  const attemptsByEventId = new Map<string, number>();

  const handler = (req: IncomingMessage, res: ServerResponse): void => {
    const eventId = req.headers["webhook-event-id"] as string;

    if (processedEventIds.has(eventId)) {
      res.writeHead(200); // idempotent no-op — already successfully processed
      res.end();
      return;
    }

    const attemptNumber = (attemptsByEventId.get(eventId) ?? 0) + 1;
    attemptsByEventId.set(eventId, attemptNumber);

    if (shouldSucceed(eventId, attemptNumber)) {
      processedEventIds.add(eventId);
      res.writeHead(200);
      res.end();
    } else {
      res.writeHead(500);
      res.end();
    }
  };

  return { handler, processedEventIds, attemptsByEventId };
}
