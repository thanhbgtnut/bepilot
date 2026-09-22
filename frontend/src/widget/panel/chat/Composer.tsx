import { useRef, useState } from "react";

interface ComposerProps {
  onSend: (text: string) => void;
  disabled?: boolean;
}

export function Composer({ onSend, disabled }: ComposerProps) {
  const [value, setValue] = useState("");
  // Mirror of `value` for synchronous reads: a fast double Enter (IME commit +
  // key event, key repeat, …) can run the keydown handler twice before React
  // flushes the cleared state, and a stale `value` closure would send twice.
  const valueRef = useRef("");
  const composingRef = useRef(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  function setText(next: string) {
    valueRef.current = next;
    setValue(next);
  }

  function autoGrow() {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 96)}px`;
  }

  function submit() {
    const text = valueRef.current.trim();
    if (!text) return;
    setText(""); // clears the ref synchronously → a second sync call is a no-op
    onSend(text);
    requestAnimationFrame(autoGrow);
  }

  return (
    <form
      className="border-t border-border bg-surface px-3 py-2.5"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <div className="flex items-end gap-2">
        <textarea
          ref={textareaRef}
          rows={1}
          value={value}
          disabled={disabled}
          placeholder="Nhắn cho Trợ lý Thẩm định…"
          aria-label="Soạn tin nhắn"
          onChange={(event) => {
            setText(event.target.value);
            autoGrow();
          }}
          onCompositionStart={() => {
            composingRef.current = true;
          }}
          onCompositionEnd={() => {
            composingRef.current = false;
          }}
          onKeyDown={(event) => {
            // Ignore Enter that only commits an IME composition (Vietnamese
            // Telex/VNI, etc.) — it must not also send the message.
            if (
              event.key === "Enter" &&
              !event.shiftKey &&
              !event.nativeEvent.isComposing &&
              !composingRef.current
            ) {
              event.preventDefault();
              submit();
            }
          }}
          className="max-h-24 flex-1 resize-none rounded-lg border border-border-strong bg-surface px-2.5 py-2 text-[13px] text-text focus-visible:outline-2 focus-visible:outline-accent focus-visible:outline-offset-0 disabled:opacity-60"
        />
        <button
          type="submit"
          aria-label="Gửi"
          disabled={disabled}
          className="grid size-9 flex-none cursor-pointer place-items-center rounded-lg border border-accent bg-accent text-white focus-visible:outline-2 focus-visible:outline-accent focus-visible:outline-offset-2 disabled:cursor-not-allowed disabled:opacity-60"
        >
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path
              d="M2 8 14 3l-4 12-2.6-4.6L2 8Z"
              stroke="currentColor"
              strokeWidth="1.4"
              strokeLinejoin="round"
            />
          </svg>
        </button>
      </div>
      <div className="mt-1.5 text-[10px] text-text-muted">
        Trợ lý hỗ trợ chuẩn bị &amp; phân tích sơ bộ. Quyết định cấp tín dụng
        vẫn do chuyên viên &amp; cấp phê duyệt.
      </div>
    </form>
  );
}
