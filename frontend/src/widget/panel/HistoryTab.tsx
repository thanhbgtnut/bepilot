import type { SessionSummary } from "../types";
import { formatDateTime } from "../utils";

interface HistoryTabProps {
  sessions: SessionSummary[];
  loading: boolean;
  error: string | null;
  activeSessionId: string;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onRefresh: () => void;
}

export function HistoryTab({
  sessions,
  loading,
  error,
  activeSessionId,
  onSelectSession,
  onNewSession,
  onRefresh,
}: HistoryTabProps) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex-1 overflow-y-auto px-4 pt-4 pb-5">
        <div className="mb-3.5 flex items-center justify-between gap-2">
          <p className="text-[13px]">Các cuộc trò chuyện trước đây với Trợ lý.</p>
          <button
            type="button"
            onClick={onRefresh}
            disabled={loading}
            className="shrink-0 cursor-pointer text-[11px] font-semibold text-accent disabled:cursor-default disabled:opacity-50"
          >
            Làm mới
          </button>
        </div>

        <button
          type="button"
          onClick={onNewSession}
          className="mb-4 w-full cursor-pointer rounded-lg border border-dashed border-border-strong px-3 py-2.5 text-center text-xs font-semibold text-text focus-visible:outline-2 focus-visible:outline-accent focus-visible:outline-offset-2"
        >
          + Cuộc trò chuyện mới
        </button>

        {error && (
          <div className="mb-3 rounded-lg border border-crit/35 bg-crit-soft px-2.5 py-2 text-[12.5px] text-crit">
            Không tải được lịch sử: {error}
          </div>
        )}

        {loading && sessions.length === 0 && (
          <p className="text-center text-[12.5px] text-text-muted">Đang tải…</p>
        )}

        {!loading && !error && sessions.length === 0 && (
          <p className="text-center text-[12.5px] text-text-muted">
            Chưa có cuộc trò chuyện nào.
          </p>
        )}

        <div className="flex flex-col gap-1.5">
          {sessions.map((session) => (
            <button
              key={session.id}
              type="button"
              onClick={() => onSelectSession(session.id)}
              aria-current={session.id === activeSessionId}
              className={`cursor-pointer rounded-lg border px-2.5 py-2 text-left text-[12.5px] focus-visible:outline-2 focus-visible:outline-accent focus-visible:outline-offset-2 ${
                session.id === activeSessionId
                  ? "border-accent bg-accent/10"
                  : "border-border bg-surface-2 hover:border-border-strong"
              }`}
            >
              <div className="truncate font-semibold">{session.title}</div>
              <div className="mono mt-0.5 text-[11px] text-text-muted">
                {formatDateTime(session.updatedAt)}
              </div>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
