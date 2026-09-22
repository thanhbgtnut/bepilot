export interface WidgetConfig {
  agentName: string;
  agentInitials: string;
  agentRole: string;
  userInitials: string;
  loanId: string;
  borrowerName: string;
  /** AG-UI protocol endpoint the chat tab connects to (HttpAgent url). */
  agentUrl: string;
  /** Extra HTTP headers sent with every AG-UI request (e.g. an auth token). */
  agentHeaders?: Record<string, string>;
  pendingCount: number;
  /** Delay in ms before the dock auto-opens on load. Set 0 to disable. */
  autoOpenDelay: number;
  /** Delay in ms before the nudge callout appears if the dock stays closed. Set 0 to disable. */
  nudgeDelay: number;
}

export type MessageSide = "ai" | "me";

/**
 * Rendering model the chat UI draws from — deliberately generic since it's
 * populated from live AG-UI messages, not scripted content. A "paragraph" is
 * plain assistant/user text; a "card" is the best-effort rendering of a tool
 * call (heading = tool name, kv = parsed flat arguments, note = raw
 * arguments/result when they don't parse into flat key-values).
 */
export type MessageBlock =
  | { type: "paragraph"; html: string }
  | {
      type: "card";
      heading: string;
      kv?: { label: string; value: string }[];
      note?: string;
    };

export interface ChatMessage {
  id: string;
  side: MessageSide;
  blocks: MessageBlock[];
}

/** One entry in the chat history list (GET /v1/sessions), keyed by AG-UI threadId. */
export interface SessionSummary {
  id: string;
  title: string;
  updatedAt: string;
}

/**
 * One task result saved by a skill's `save_task_result` tool call (GET
 * /v1/sessions/{id}/report). `result` is whatever JSON shape that skill's
 * instructions define — rendered generically, not by task_key, so any
 * skill's saved output shows up here without frontend changes.
 */
export interface TaskResult {
  taskKey: string;
  title: string;
  result: unknown;
  updatedAt: string;
}

export interface SessionReport {
  id: string;
  title: string;
  updatedAt: string;
  tasks: TaskResult[];
}
