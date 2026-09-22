import { useState } from "react";

const SUGGESTED_TASKS = [
  "Phân tích độ nhạy DSCR theo giá nguyên liệu đầu vào",
  "Đối chiếu hợp đồng đầu ra với doanh thu ghi nhận 2025",
  "Tra soát toàn bộ giao dịch bên liên quan 24 tháng",
  "Chuẩn bị phiếu phỏng vấn KH và đề cương kiểm tra thực địa",
  "Chuẩn hóa nhóm khoản mục phải thu và lập bảng tuổi nợ",
];

const PRIORITIES = ["Bình thường", "Cao", "Gấp"];

const FIELD_CLASS =
  "w-full rounded-md border border-border-strong bg-surface px-2 py-1.5 text-[12.5px] text-text focus-visible:outline-2 focus-visible:outline-accent";

interface AssignTaskFormProps {
  onSubmit: (task: string, priority: string, note: string) => void;
  onCancel: () => void;
}

export function AssignTaskForm({ onSubmit, onCancel }: AssignTaskFormProps) {
  const [task, setTask] = useState(SUGGESTED_TASKS[0]);
  const [priority, setPriority] = useState(PRIORITIES[0]);
  const [note, setNote] = useState("");

  return (
    <div className="mb-2.5 overflow-hidden rounded-lg border border-border bg-surface-2">
      <div className="border-b border-border px-2.5 py-1.5 text-[10.5px] font-semibold tracking-wide text-text-muted uppercase">
        Giao đầu việc cho Trợ lý
      </div>
      <div className="flex flex-col gap-2.5 px-2.5 py-2.5">
        <div>
          <label className="mb-1 block text-[10.5px] text-text-muted">
            Đầu việc
          </label>
          <select
            value={task}
            onChange={(event) => setTask(event.target.value)}
            className={FIELD_CLASS}
          >
            {SUGGESTED_TASKS.map((option) => (
              <option key={option}>{option}</option>
            ))}
          </select>
        </div>
        <div>
          <label className="mb-1 block text-[10.5px] text-text-muted">
            Mức ưu tiên
          </label>
          <select
            value={priority}
            onChange={(event) => setPriority(event.target.value)}
            className={FIELD_CLASS}
          >
            {PRIORITIES.map((option) => (
              <option key={option}>{option}</option>
            ))}
          </select>
        </div>
        <div>
          <label className="mb-1 block text-[10.5px] text-text-muted">
            Ghi chú (tuỳ chọn)
          </label>
          <input
            value={note}
            onChange={(event) => setNote(event.target.value)}
            placeholder="Ví dụ: cần trước cuộc họp thẩm định"
            className={FIELD_CLASS}
          />
        </div>
      </div>
      <div className="flex justify-end gap-2 border-t border-border bg-surface px-2.5 py-2">
        <button
          type="button"
          onClick={onCancel}
          className="cursor-pointer rounded-md border border-border-strong bg-surface px-2.5 py-1.5 text-xs font-semibold text-text focus-visible:outline-2 focus-visible:outline-accent focus-visible:outline-offset-2"
        >
          Huỷ
        </button>
        <button
          type="button"
          onClick={() => onSubmit(task, priority, note.trim())}
          className="cursor-pointer rounded-md border border-accent bg-accent px-2.5 py-1.5 text-xs font-semibold text-white focus-visible:outline-2 focus-visible:outline-accent focus-visible:outline-offset-2"
        >
          Giao việc
        </button>
      </div>
    </div>
  );
}
