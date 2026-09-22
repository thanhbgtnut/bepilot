import type { SessionReport } from "../types";

interface RawTaskResult {
  task_key: string;
  title: string;
  result: unknown;
  updated_at: string;
}

interface RawSessionReport {
  id: string;
  title: string;
  updated_at: string;
  tasks: RawTaskResult[];
}

/**
 * bepilot's saved-task-results endpoint (GET /v1/sessions/{id}/report), a
 * REST extension like fetchSessionHistory — reached the same way (origin
 * derived from the AG-UI agent URL), not through the AG-UI protocol. The
 * backend serves it straight from Postgres, independent of the agent run
 * that produced the data, so it works even if a live run is failing.
 */
function apiOrigin(agentUrl: string): string {
  return new URL(agentUrl).origin;
}

/** Thrown when the session has no report yet (e.g. a brand-new conversation
 * that has not been sent to the backend, so it has no row to 404 on). */
export class SessionReportNotFoundError extends Error {}

export async function fetchSessionReport(
  agentUrl: string,
  headers: Record<string, string> | undefined,
  sessionId: string,
): Promise<SessionReport> {
  const res = await fetch(`${apiOrigin(agentUrl)}/v1/sessions/${sessionId}/report`, { headers });
  if (res.status === 404) {
    throw new SessionReportNotFoundError(`session ${sessionId} not found`);
  }
  if (!res.ok) {
    throw new Error(`GET /v1/sessions/${sessionId}/report failed: HTTP ${res.status}`);
  }
  const body = (await res.json()) as RawSessionReport;
  return {
    id: body.id,
    title: body.title.trim() || "Cuộc trò chuyện chưa đặt tên",
    updatedAt: body.updated_at,
    tasks: body.tasks.map((t) => ({
      taskKey: t.task_key,
      title: t.title,
      result: t.result,
      updatedAt: t.updated_at,
    })),
  };
}
