export type PanelTabId = "chat" | "report" | "history";

const TABS: { id: PanelTabId; label: string }[] = [
  { id: "chat", label: "Trao đổi" },
  { id: "report", label: "Báo cáo đầy đủ" },
  { id: "history", label: "Lịch sử" },
];

interface PanelTabsProps {
  active: PanelTabId;
  onChange: (tab: PanelTabId) => void;
}

export function PanelTabs({ active, onChange }: PanelTabsProps) {
  return (
    <div role="tablist" className="flex gap-0.5 border-b border-border bg-surface px-4">
      {TABS.map((tab) => (
        <button
          key={tab.id}
          role="tab"
          aria-selected={active === tab.id}
          onClick={() => onChange(tab.id)}
          className={`-mb-px cursor-pointer border-b-2 px-3 py-2.5 text-xs font-semibold focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent ${
            active === tab.id
              ? "border-accent text-text"
              : "border-transparent text-text-muted"
          }`}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}
