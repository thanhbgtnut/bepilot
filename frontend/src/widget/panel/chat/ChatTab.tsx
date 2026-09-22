import { useEffect, useRef, useState } from "react";
import type { AgentChatStatus } from "../../agent/useAgentChat";
import type { ChatMessage } from "../../types";
import { AssignTaskForm } from "./AssignTaskForm";
import { Composer } from "./Composer";
import { MessageBubble } from "./MessageBubble";
import { QuickReplies } from "./QuickReplies";
import { TypingIndicator } from "./TypingIndicator";

const SUGGESTED_PROMPTS = [
  "Chèn tờ trình nháp vào hồ sơ",
  "DSCR ở kịch bản xấu là bao nhiêu?",
  "Giải trình khoản phải thu khác tăng đột biến Q4",
  "Cho mình xem chi tiết mục thẩm quyền vay",
];

interface ChatTabProps {
  messages: ChatMessage[];
  status: AgentChatStatus;
  errorMessage: string | null;
  sendMessage: (text: string) => void;
  agentInitials: string;
  userInitials: string;
  active: boolean;
  onOpenReport: () => void;
}

export function ChatTab({
  messages,
  status,
  errorMessage,
  sendMessage,
  agentInitials,
  userInitials,
  active,
  onOpenReport,
}: ChatTabProps) {
  const [showAssignForm, setShowAssignForm] = useState(false);
  const threadRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    threadRef.current?.scrollTo({ top: threadRef.current.scrollHeight });
  }, [messages, status]);

  useEffect(() => {
    if (active) threadRef.current?.scrollTo({ top: threadRef.current.scrollHeight });
  }, [active]);

  function handleAssignSubmit(task: string, priority: string, note: string) {
    setShowAssignForm(false);
    sendMessage(`Giao việc: ${task} · ưu tiên ${priority}${note ? ` · ${note}` : ""}`);
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div
        ref={threadRef}
        role="log"
        aria-label="Trao đổi với Trợ lý Thẩm định"
        className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto bg-bg p-4"
      >
        {messages.length === 0 && status !== "sending" && (
          <p className="mt-6 text-center text-[12.5px] text-text-muted">
            Bắt đầu trao đổi với Trợ lý Thẩm định bên dưới.
          </p>
        )}

        {messages.map((message) => (
          <MessageBubble
            key={message.id}
            message={message}
            agentInitials={agentInitials}
            userInitials={userInitials}
          />
        ))}

        {status === "sending" && messages[messages.length - 1]?.side !== "ai" && (
          <TypingIndicator agentInitials={agentInitials} />
        )}

        {status === "error" && errorMessage && (
          <div className="rounded-lg border border-crit/35 bg-crit-soft px-3 py-2 text-[12.5px] text-crit">
            Không thể kết nối tới Trợ lý: {errorMessage}
          </div>
        )}
      </div>

      {showAssignForm && (
        <div className="border-t border-border bg-surface px-3 pt-2.5">
          <AssignTaskForm
            onSubmit={handleAssignSubmit}
            onCancel={() => setShowAssignForm(false)}
          />
        </div>
      )}

      <QuickReplies
        prompts={SUGGESTED_PROMPTS}
        onSelectPrompt={sendMessage}
        onOpenAssignForm={() => setShowAssignForm(true)}
        onOpenReport={onOpenReport}
      />
      <Composer onSend={sendMessage} disabled={status === "sending"} />
    </div>
  );
}
