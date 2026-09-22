interface TypingIndicatorProps {
  agentInitials: string;
}

export function TypingIndicator({ agentInitials }: TypingIndicatorProps) {
  return (
    <div className="grid max-w-[90%] grid-cols-[27px_minmax(0,1fr)] gap-2.5">
      <div className="flex size-[27px] flex-none items-center justify-center rounded-full bg-gradient-to-br from-accent to-accent-ink font-serif text-[11px] font-semibold text-white">
        {agentInitials}
      </div>
      <div className="flex items-center gap-1 rounded-xl rounded-tl-sm border border-border bg-surface px-3 py-2.5">
        <i className="aaw-typing-dot size-[5px] rounded-full bg-text-muted" />
        <i
          className="aaw-typing-dot size-[5px] rounded-full bg-text-muted"
          style={{ animationDelay: "0.15s" }}
        />
        <i
          className="aaw-typing-dot size-[5px] rounded-full bg-text-muted"
          style={{ animationDelay: "0.3s" }}
        />
      </div>
    </div>
  );
}
