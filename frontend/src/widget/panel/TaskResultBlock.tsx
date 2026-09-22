import { useState } from "react";
import type { ReactNode } from "react";
import type { TaskResult } from "../types";

interface ChecklistResult {
  items: { label: string; status: string; note?: string }[];
}

interface FieldsResult {
  fields: { label: string; value: string }[];
}

function isChecklistResult(value: unknown): value is ChecklistResult {
  return !!value && typeof value === "object" && Array.isArray((value as ChecklistResult).items);
}

function isFieldsResult(value: unknown): value is FieldsResult {
  return !!value && typeof value === "object" && Array.isArray((value as FieldsResult).fields);
}

const STATUS_LABEL: Record<string, string> = {
  ok: "Đủ",
  partial: "Thiếu một phần",
  missing: "Thiếu",
};

const STATUS_STYLE: Record<string, string> = {
  ok: "bg-good-soft text-good",
  partial: "bg-warn-soft text-warn",
  missing: "bg-crit-soft text-crit",
};

/** Renders the "Kết quả" section's payload generically, by shape. */
function ResultBody({ value }: { value: unknown }) {
  if (isChecklistResult(value)) {
    return (
      <div className="flex flex-col gap-1.5">
        {value.items.map((item, i) => (
          <div
            key={i}
            className="grid grid-cols-[auto_1fr] items-start gap-x-2 gap-y-0.5 rounded-lg border border-border bg-surface-2 px-2.5 py-2 text-[12.5px]"
          >
            <span
              className={`rounded px-1.5 py-0.5 text-[10px] font-semibold whitespace-nowrap ${
                STATUS_STYLE[item.status] ?? "bg-surface-2 text-text-muted"
              }`}
            >
              {STATUS_LABEL[item.status] ?? item.status}
            </span>
            <span>{item.label}</span>
            {item.note && (
              <span className="col-start-2 text-[11px] text-text-muted">{item.note}</span>
            )}
          </div>
        ))}
      </div>
    );
  }

  if (isFieldsResult(value)) {
    return (
      <div className="overflow-hidden rounded-lg border border-border">
        {value.fields.map((field, i) => (
          <div
            key={i}
            className={`grid grid-cols-[auto_1fr] gap-3 px-2.5 py-2 text-[12.5px] ${
              i % 2 === 1 ? "bg-surface-2" : ""
            }`}
          >
            <span className="text-text-muted">{field.label}</span>
            <span className="mono text-right">{field.value}</span>
          </div>
        ))}
      </div>
    );
  }

  if (typeof value === "string") {
    return <p className="text-[12.5px]">{value}</p>;
  }

  return (
    <pre className="overflow-x-auto rounded-lg border border-border bg-surface-2 px-2.5 py-2 text-[11px]">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}

interface ParsedTask {
  summary: string;
  result: unknown;
  sources: string[];
}

/**
 * A task is expected to be saved as `{summary, result, sources}` (see
 * skills/tham-dinh-phuong-an/SKILL.md's "Lưu kết quả có cấu trúc" section —
 * any skill's save_task_result call should follow the same 3-part shape).
 * Anything else is still rendered as the "Kết quả" section alone, so an
 * older or non-conforming save doesn't just disappear.
 */
function parseTask(raw: unknown): ParsedTask {
  if (raw && typeof raw === "object" && !Array.isArray(raw)) {
    const obj = raw as Record<string, unknown>;
    if ("summary" in obj || "sources" in obj) {
      const sources = Array.isArray(obj.sources)
        ? obj.sources.filter((s): s is string => typeof s === "string")
        : [];
      return {
        summary: typeof obj.summary === "string" ? obj.summary : "",
        result: "result" in obj ? obj.result : raw,
        sources,
      };
    }
  }
  return { summary: "", result: raw, sources: [] };
}

function SectionLabel({ children }: { children: ReactNode }) {
  return (
    <p className="mb-1.5 text-[11px] font-semibold tracking-wide text-text-muted uppercase">
      {children}
    </p>
  );
}

interface TaskResultBlockProps {
  index: number;
  task: TaskResult;
  defaultOpen: boolean;
}

/**
 * One collapsible task block, mirroring the accordion "task" pattern from
 * the original report-demo.html mock: an always-visible header (index +
 * title) that expands into three sections — Tổng hợp chung, Kết quả, Nguồn
 * thông tin — so the report stays legible as more task types are added.
 */
export function TaskResultBlock({ index, task, defaultOpen }: TaskResultBlockProps) {
  const [open, setOpen] = useState(defaultOpen);
  const parsed = parseTask(task.result);

  return (
    <article className="overflow-hidden rounded-lg border border-border bg-surface shadow-sm">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="grid w-full grid-cols-[24px_1fr_auto] items-center gap-3 px-3.5 py-3 text-left hover:bg-surface-2 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
      >
        <span className="mono text-[11px] text-text-muted">
          {String(index + 1).padStart(2, "0")}
        </span>
        <span className="min-w-0 truncate text-[13.5px] font-semibold">{task.title}</span>
        <svg
          viewBox="0 0 14 14"
          fill="none"
          aria-hidden="true"
          className={`size-3.5 flex-none text-text-muted transition-transform ${open ? "rotate-90" : ""}`}
        >
          <path
            d="M5 3l4 4-4 4"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>

      {open && (
        <div className="border-t border-border px-3.5 py-3.5 pl-[52px]">
          {parsed.summary && (
            <div className="mb-3.5">
              <SectionLabel>Tổng hợp chung</SectionLabel>
              <p className="text-[12.5px]">{parsed.summary}</p>
            </div>
          )}

          <div className="mb-3.5">
            <SectionLabel>Kết quả</SectionLabel>
            <ResultBody value={parsed.result} />
          </div>

          {parsed.sources.length > 0 && (
            <div>
              <SectionLabel>Nguồn thông tin</SectionLabel>
              <div className="flex flex-wrap gap-1.5">
                {parsed.sources.map((src, i) => (
                  <span
                    key={i}
                    className="rounded-md border border-border bg-surface-2 px-2 py-1 text-[10.5px] text-text-muted"
                  >
                    {src}
                  </span>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </article>
  );
}
