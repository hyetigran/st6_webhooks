/** Handles both directions: a past timestamp (e.g. oldest_pending_at)
 * reads "Xs ago"; a future one (e.g. a scheduled next_attempt_at) reads
 * "in Xs" — callers pass both through this one function, so it can't
 * silently clamp a future time to "0s ago" the way a past-only formatter
 * would. */
export function formatRelativeTime(iso: string | null): string {
  if (!iso) return "—";
  const deltaMs = Date.now() - new Date(iso).getTime();
  const isFuture = deltaMs < 0;
  const magnitude = magnitudeLabel(Math.round(Math.abs(deltaMs) / 1000));
  return isFuture ? `in ${magnitude}` : `${magnitude} ago`;
}

function magnitudeLabel(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.round(hours / 24);
  return `${days}d`;
}

export function formatPercent(fraction: number | null): string {
  if (fraction === null) return "—";
  return `${Math.round(fraction * 100)}%`;
}

export function formatDateTime(iso: string): string {
  return new Date(iso).toISOString().replace("T", " ").replace(/\.\d+Z$/, "Z");
}
