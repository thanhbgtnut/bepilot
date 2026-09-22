interface PanelHeaderProps {
  agentInitials: string;
  agentName: string;
  agentRole: string;
  onClose: () => void;
}

export function PanelHeader({
  agentInitials,
  agentName,
  agentRole,
  onClose,
}: PanelHeaderProps) {
  return (
    <div className="flex items-start gap-2.5 border-b border-border px-4 py-3.5">
      <div className="flex size-[38px] flex-none items-center justify-center rounded-[10px] bg-gradient-to-br from-accent to-accent-ink font-serif text-base font-semibold text-white">
        {agentInitials}
      </div>
      <div className="min-w-0 flex-1">
        <b className="flex items-center gap-1.5 text-sm">
          {agentName}
          <span
            className="aaw-live size-[7px] rounded-full bg-good"
            title="Đang hoạt động"
          />
        </b>
        <div className="text-[11.5px] text-text-muted">{agentRole}</div>
      </div>
      <button
        type="button"
        onClick={onClose}
        aria-label="Đóng"
        className="grid size-7 flex-none cursor-pointer place-items-center rounded-md border border-border bg-surface text-[15px] text-text-muted focus-visible:outline-2 focus-visible:outline-accent focus-visible:outline-offset-2"
      >
        ✕
      </button>
    </div>
  );
}
