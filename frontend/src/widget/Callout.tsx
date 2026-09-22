interface CalloutProps {
  agentInitials: string;
  agentName: string;
  loanId: string;
  onOpen: () => void;
  onDismiss: () => void;
}

export function Callout({
  agentInitials,
  agentName,
  loanId,
  onOpen,
  onDismiss,
}: CalloutProps) {
  return (
    <div className="aaw-callout-in fixed right-5.5 bottom-19.5 z-40 w-[300px] max-w-[calc(100vw-24px)] rounded-xl border border-border-strong bg-surface p-3.5 pb-3 shadow-lg after:absolute after:-bottom-1.5 after:right-6.5 after:size-3 after:rotate-45 after:border-r after:border-b after:border-border-strong after:bg-surface">
      <div className="mb-1.5 flex items-center gap-2">
        <div className="flex size-6.5 items-center justify-center rounded-full bg-gradient-to-br from-accent to-accent-ink font-serif text-xs font-semibold text-white">
          {agentInitials}
        </div>
        <b className="text-xs">{agentName}</b>
      </div>
      <p className="mb-2.5 text-xs">
        Chào chị Hà — em đã chuẩn bị sẵn phần thẩm định sơ bộ cho hồ sơ{" "}
        <span className="mono">{loanId}</span>. Chị trao đổi với em nhé?
      </p>
      <div className="flex gap-2">
        <button
          type="button"
          onClick={onOpen}
          className="cursor-pointer rounded-md border border-accent bg-accent px-2.5 py-1.5 text-xs font-semibold text-white"
        >
          Xem kết quả
        </button>
        <button
          type="button"
          onClick={onDismiss}
          className="cursor-pointer rounded-md border border-border-strong bg-transparent px-2.5 py-1.5 text-xs font-semibold text-text-muted"
        >
          Để sau
        </button>
      </div>
    </div>
  );
}
