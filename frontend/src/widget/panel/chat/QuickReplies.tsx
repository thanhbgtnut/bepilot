const CHIP_CLASS =
  "cursor-pointer rounded-full border border-border-strong bg-surface px-2.5 py-1.5 text-[11.5px] font-medium text-text hover:bg-surface-2 focus-visible:outline-2 focus-visible:outline-accent focus-visible:outline-offset-2";

interface QuickRepliesProps {
  prompts: string[];
  onSelectPrompt: (text: string) => void;
  onOpenAssignForm: () => void;
  onOpenReport: () => void;
}

export function QuickReplies({
  prompts,
  onSelectPrompt,
  onOpenAssignForm,
  onOpenReport,
}: QuickRepliesProps) {
  return (
    <div className="flex flex-wrap gap-1.5 bg-surface px-3 pt-2.5">
      <button type="button" onClick={onOpenAssignForm} className={CHIP_CLASS}>
        Giao thêm đầu việc
      </button>
      <button type="button" onClick={onOpenReport} className={CHIP_CLASS}>
        Xem báo cáo đầy đủ
      </button>
      {prompts.map((prompt) => (
        <button
          key={prompt}
          type="button"
          onClick={() => onSelectPrompt(prompt)}
          className={CHIP_CLASS}
        >
          {prompt}
        </button>
      ))}
    </div>
  );
}
