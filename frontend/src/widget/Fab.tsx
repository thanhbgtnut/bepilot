interface FabProps {
  agentInitials: string;
  agentName: string;
  pendingCount: number;
  onClick: () => void;
}

export function Fab({ agentInitials, agentName, pendingCount, onClick }: FabProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-haspopup="dialog"
      className="fixed right-5.5 bottom-5.5 z-40 flex cursor-pointer items-center gap-2.5 rounded-full border border-border-strong bg-surface py-2 pr-4 pl-2 text-text shadow-lg"
    >
      <span className="relative flex size-[34px] flex-none items-center justify-center rounded-full bg-gradient-to-br from-accent to-accent-ink font-serif text-sm font-semibold text-white">
        {agentInitials}
        {pendingCount > 0 && (
          <span className="absolute -top-[3px] -right-[3px] grid h-[17px] min-w-[17px] place-items-center rounded-full border-2 border-surface bg-crit px-1 text-[10px] font-bold text-white">
            {pendingCount}
          </span>
        )}
      </span>
      <span className="hidden text-left sm:block">
        <b className="block text-xs">{agentName}</b>
        <span className="block text-[10.5px] text-text-muted">
          {pendingCount > 0 ? `${pendingCount} việc cần bạn xác nhận` : "Đang hoạt động"}
        </span>
      </span>
    </button>
  );
}
