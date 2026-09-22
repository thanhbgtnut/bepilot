import { HttpAgent, randomUUID } from "@ag-ui/client";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ChatMessage } from "../types";
import { mapAgMessagesToChat } from "./mapMessages";

export type AgentChatStatus = "idle" | "sending" | "error";

interface UseAgentChatOptions {
  url: string;
  headers?: Record<string, string>;
}

interface UseAgentChatResult {
  messages: ChatMessage[];
  status: AgentChatStatus;
  errorMessage: string | null;
  /** AG-UI threadId of the conversation currently loaded (maps to a bepilot session id). */
  activeThreadId: string;
  sendMessage: (text: string) => void;
  /** Loads an existing session's transcript and continues it on send. */
  switchToSession: (threadId: string) => void;
  /** Clears the panel back to a fresh, empty conversation. */
  startNewSession: () => void;
}

/**
 * Bridges the chat UI to a real AG-UI agent over HTTP. One HttpAgent
 * instance is created per widget session and kept for its lifetime, so the
 * conversation (agent.messages / threadId) persists across tab switches.
 */
export function useAgentChat({ url, headers }: UseAgentChatOptions): UseAgentChatResult {
  // Lazy initializer runs exactly once, on mount — the standard React way to
  // construct a value one time without recreating it on every render.
  const [agent] = useState(() => new HttpAgent({ url, headers }));

  const [messages, setMessages] = useState<ChatMessage[]>(() =>
    mapAgMessagesToChat(agent.messages),
  );
  const [status, setStatus] = useState<AgentChatStatus>("idle");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [activeThreadId, setActiveThreadId] = useState(agent.threadId);
  // Synchronous in-flight guard: `status` only flips to "sending" once the
  // backend's RUN_STARTED arrives, so a rapid second send (double keydown,
  // impatient click) would slip through a `status`-only check.
  const runningRef = useRef(false);

  useEffect(() => {
    const { unsubscribe } = agent.subscribe({
      onMessagesChanged: ({ messages: agentMessages }) => {
        setMessages(mapAgMessagesToChat(agentMessages));
      },
      onRunStartedEvent: ({ event }) => {
        setStatus("sending");
        setErrorMessage(null);
        setActiveThreadId(event.threadId);
      },
      onRunFinishedEvent: () => {
        runningRef.current = false;
        setStatus("idle");
      },
      onRunErrorEvent: ({ event }) => {
        runningRef.current = false;
        setStatus("error");
        setErrorMessage(event.message);
      },
    });
    return unsubscribe;
  }, [agent]);

  const sendMessage = useCallback(
    (text: string) => {
      const trimmed = text.trim();
      if (!trimmed || runningRef.current) return;
      runningRef.current = true;

      agent.addMessage({ id: randomUUID(), role: "user", content: trimmed });
      setMessages(mapAgMessagesToChat(agent.messages));

      agent.runAgent({}).catch((error: unknown) => {
        runningRef.current = false;
        setStatus("error");
        setErrorMessage(error instanceof Error ? error.message : String(error));
      });
    },
    [agent],
  );

  // Loads an existing session's transcript by asking the backend to replay
  // it: set the agent's threadId to that session and run with no local
  // messages, which bepilot's AG-UI endpoint recognizes as a history replay
  // request and answers with a MESSAGES_SNAPSHOT instead of running a turn
  // (see internal/api/agui.go's aguiReplay).
  const switchToSession = useCallback(
    (threadId: string) => {
      if (threadId === agent.threadId || runningRef.current) return;
      runningRef.current = true;

      agent.threadId = threadId;
      agent.setMessages([]);
      setMessages([]);
      setActiveThreadId(threadId);

      agent.runAgent({}).catch((error: unknown) => {
        runningRef.current = false;
        setStatus("error");
        setErrorMessage(error instanceof Error ? error.message : String(error));
      });
    },
    [agent],
  );

  const startNewSession = useCallback(() => {
    agent.threadId = randomUUID();
    agent.setMessages([]);
    setMessages([]);
    setStatus("idle");
    setErrorMessage(null);
    setActiveThreadId(agent.threadId);
  }, [agent]);

  return {
    messages,
    status,
    errorMessage,
    activeThreadId,
    sendMessage,
    switchToSession,
    startNewSession,
  };
}
