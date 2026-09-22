import type { SessionReport } from "../types";
import { formatDateTime } from "../utils";
import { TaskResultBlock } from "./TaskResultBlock";

interface ReportTabProps {
  report: SessionReport | null;
  loading: boolean;
  notFound: boolean;
  error: string | null;
  loanId: string;
  active: boolean;
}

/**
 * Content comes from GET /v1/sessions/{id}/report — Postgres task_results
 * saved by the agent's save_task_result tool — not the live AG-UI stream, so
 * this tab keeps showing the last saved results even if a run is failing.
 * Each task renders as its own expand/collapse block (see TaskResultBlock),
 * so adding more task types later needs no layout change here.
 */
export function ReportTab({ report, loading, notFound, error, loanId, active }: ReportTabProps) {
  return (
    <div className={`flex min-h-0 flex-1 flex-col bg-bg ${active ? "" : "hidden"}`}>
      <div className="flex items-center justify-between gap-2.5 border-b border-border bg-surface px-3.5 py-2 text-[11.5px] text-text-muted">
        <span>
          Kết quả Trợ lý Thẩm định · hồ sơ <span className="mono">{loanId}</span>
        </span>
        {report && <span className="mono">{formatDateTime(report.updatedAt)}</span>}
      </div>

      <div className="flex-1 overflow-y-auto px-4 pt-4 pb-5">
        {loading && (
          <p className="mt-6 text-center text-[12.5px] text-text-muted">Đang tải báo cáo…</p>
        )}

        {!loading && error && (
          <div className="rounded-lg border border-crit/35 bg-crit-soft px-3 py-2 text-[12.5px] text-crit">
            Không tải được báo cáo: {error}
          </div>
        )}

        {!loading && notFound && (
          <p className="mt-6 text-center text-[12.5px] text-text-muted">
            Chưa có báo cáo cho cuộc trò chuyện này — hãy trao đổi với Trợ lý trước.
          </p>
        )}

        {!loading && report && report.tasks.length === 0 && (
          <p className="mt-6 text-center text-[12.5px] text-text-muted">
            Trợ lý chưa lưu kết quả nào cho cuộc trò chuyện này.
          </p>
        )}

        {report && report.tasks.length > 0 && (
          <>
            <div className="mb-3.5 flex items-baseline justify-between gap-3">
              <p className="truncate text-[13px] font-semibold">Công việc Trợ lý đã thực hiện</p>
              <span className="shrink-0 text-[11px] text-text-muted">
                Nhấn từng dòng để xem chi tiết
              </span>
            </div>
            <div className="flex flex-col gap-2.5">
              {report.tasks.map((task, i) => (
                <TaskResultBlock key={task.taskKey} index={i} task={task} defaultOpen={i === 0} />
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
