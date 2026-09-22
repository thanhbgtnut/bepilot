import { useCallback, useEffect, useState } from "react";
import type { SessionSummary } from "../types";
import { fetchSessionHistory } from "./sessions";

interface UseSessionHistoryOptions {
  agentUrl: string;
  headers?: Record<string, string>;
  /** Refetches whenever this flips to true — e.g. the history tab becoming active. */
  active: boolean;
}

interface UseSessionHistoryResult {
  sessions: SessionSummary[];
  loading: boolean;
  error: string | null;
  refresh: () => void;
}

export function useSessionHistory({
  agentUrl,
  headers,
  active,
}: UseSessionHistoryOptions): UseSessionHistoryResult {
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(() => {
    setLoading(true);
    setError(null);
    fetchSessionHistory(agentUrl, headers)
      .then(setSessions)
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setLoading(false));
  }, [agentUrl, headers]);

  useEffect(() => {
    if (active) refresh();
    // Only re-run when the tab is (re-)activated, not on every `refresh` identity change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  return { sessions, loading, error, refresh };
}
