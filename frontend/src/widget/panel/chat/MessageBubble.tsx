import type { ChatMessage } from "../../types";
import { MessageContent } from "./MessageContent";

interface MessageBubbleProps {
  message: ChatMessage;
  agentInitials: string;
  userInitials: string;
}

export function MessageBubble({
  message,
  agentInitials,
  userInitials,
}: MessageBubbleProps) {
  const isMe = message.side === "me";

  return (
    <div
      className={`grid max-w-[90%] grid-cols-[27px_minmax(0,1fr)] gap-2.5 ${
        isMe ? "ml-auto grid-cols-[minmax(0,1fr)_27px]" : ""
      }`}
    >
      <div
        className={`flex size-[27px] flex-none items-center justify-center rounded-full font-serif text-[11px] font-semibold ${
          isMe
            ? "order-2 border border-border bg-surface-2 text-text"
            : "bg-gradient-to-br from-accent to-accent-ink text-white"
        }`}
      >
        {isMe ? userInitials : agentInitials}
      </div>
      <div
        className={`rounded-xl border border-border bg-surface px-3 py-2 text-[13px] ${
          isMe
            ? "rounded-tr-sm border-accent/30 bg-accent-soft"
            : "rounded-tl-sm"
        }`}
      >
        <MessageContent blocks={message.blocks} />
      </div>
    </div>
  );
}
