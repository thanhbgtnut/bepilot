import type { SessionSummary } from "../types";

interface RawSessionBrief {
  id: string;
  title: string;
  updated_at: string;
}

interface RawSessionList {
  data: RawSessionBrief[];
}

/**
 * bepilot's session list (GET /v1/sessions) is a REST extension, not part of
 * the AG-UI protocol — so unlike the chat stream, it isn't reached through
 * `@ag-ui/client`. The widget is only ever configured with one backend, so
 * its origin is derived from the AG-UI agent URL rather than adding a second
 * config field.
 */
function apiOrigin(agentUrl: string): string {
  return new URL(agentUrl).origin;
}

/** Lists the authenticated user's chat sessions, most recently updated first. */
export async function fetchSessionHistory(
  agentUrl: string,
  headers?: Record<string, string>,
): Promise<SessionSummary[]> {
  const res = await fetch(`${apiOrigin(agentUrl)}/v1/sessions?limit=50`, { headers });
  if (!res.ok) {
    throw new Error(`GET /v1/sessions failed: HTTP ${res.status}`);
  }
  const body = (await res.json()) as RawSessionList;
  return body.data.map((s) => ({
    id: s.id,
    title: s.title.trim() || "Cuộc trò chuyện chưa đặt tên",
    updatedAt: s.updated_at,
  }));
}
