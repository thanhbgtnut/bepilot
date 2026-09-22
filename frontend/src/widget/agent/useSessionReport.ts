import { useCallback, useEffect, useState } from "react";
import type { SessionReport } from "../types";
import { fetchSessionReport, SessionReportNotFoundError } from "./report";

interface UseSessionReportOptions {
  agentUrl: string;
  headers?: Record<string, string>;
  /** The AG-UI threadId currently loaded in the chat tab. */
  sessionId: string;
  /** Refetches whenever this flips to true, or sessionId changes while true. */
  active: boolean;
}

interface UseSessionReportResult {
  report: SessionReport | null;
  loading: boolean;
  /** True once the session has been sent to the backend but has no saved report yet. */
  notFound: boolean;
  error: string | null;
  refresh: () => void;
}

export function useSessionReport({
  agentUrl,
  headers,
  sessionId,
  active,
}: UseSessionReportOptions): UseSessionReportResult {
  const [report, setReport] = useState<SessionReport | null>(null);
  const [loading, setLoading] = useState(false);
  const [notFound, setNotFound] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(() => {
    setReport(null);
    setNotFound(false);
    setError(null);
    setLoading(true);
    fetchSessionReport(agentUrl, headers, sessionId)
      .then(setReport)
      .catch((err: unknown) => {
        if (err instanceof SessionReportNotFoundError) {
          setNotFound(true);
          return;
        }
        setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => setLoading(false));
  }, [agentUrl, headers, sessionId]);

  useEffect(() => {
    if (active) refresh();
    // Re-run when the active session changes too, not just on tab activation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, sessionId]);

  return { report, loading, notFound, error, refresh };
}
