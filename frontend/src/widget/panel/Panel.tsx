import { useEffect, useState } from "react";
import { useAgentChat } from "../agent/useAgentChat";
import { useSessionHistory } from "../agent/useSessionHistory";
import { useSessionReport } from "../agent/useSessionReport";
import type { WidgetConfig } from "../types";
import { ChatTab } from "./chat/ChatTab";
import { HistoryTab } from "./HistoryTab";
import { PanelHeader } from "./PanelHeader";
import { PanelTabs, type PanelTabId } from "./PanelTabs";
import { ReportTab } from "./ReportTab";

const PANEL_MIN_WIDTH = 660;
const PANEL_MAX_WIDTH_RATIO = 0.8;
const PANEL_RESIZE_STEP = 24;

function maxPanelWidth() {
  return window.innerWidth * PANEL_MAX_WIDTH_RATIO;
}

interface PanelProps {
  config: WidgetConfig;
  userInitials: string;
  open: boolean;
  activeTab: PanelTabId;
  onTabChange: (tab: PanelTabId) => void;
  onClose: () => void;
}

export function Panel({
  config,
  userInitials,
  open,
  activeTab,
  onTabChange,
  onClose,
}: PanelProps) {
  // One AG-UI session for the panel's lifetime, shared across tabs so the
  // conversation survives switching away from and back to "Trao đổi".
  const {
    messages,
    status,
    errorMessage,
    activeThreadId,
    sendMessage,
    switchToSession,
    startNewSession,
  } = useAgentChat({
    url: config.agentUrl,
    headers: config.agentHeaders,
  });

  const {
    sessions,
    loading: historyLoading,
    error: historyError,
    refresh: refreshHistory,
  } = useSessionHistory({
    agentUrl: config.agentUrl,
    headers: config.agentHeaders,
    active: activeTab === "history",
  });

  const {
    report,
    loading: reportLoading,
    notFound: reportNotFound,
    error: reportError,
  } = useSessionReport({
    agentUrl: config.agentUrl,
    headers: config.agentHeaders,
    sessionId: activeThreadId,
    active: activeTab === "report",
  });

  function openReport() {
    onTabChange("report");
  }

  function selectSession(id: string) {
    switchToSession(id);
    onTabChange("chat");
  }

  function newSession() {
    startNewSession();
    onTabChange("chat");
  }

  // Panel defaults to its minimum width; dragging the left edge widens it up
  // to 4/5 of the viewport. Width is plain component state (not persisted) —
  // it resets to the default on next embed/reload.
  const [width, setWidth] = useState(PANEL_MIN_WIDTH);

  useEffect(() => {
    function clampToViewport() {
      setWidth((current) => Math.min(current, maxPanelWidth()));
    }
    window.addEventListener("resize", clampToViewport);
    return () => window.removeEventListener("resize", clampToViewport);
  }, []);

  function startResizing(event: React.PointerEvent<HTMLDivElement>) {
    event.preventDefault();
    const pointerX = event.clientX;
    const startWidth = width;

    function handlePointerMove(moveEvent: PointerEvent) {
      const next = startWidth + (pointerX - moveEvent.clientX);
      setWidth(Math.min(maxPanelWidth(), Math.max(PANEL_MIN_WIDTH, next)));
    }
    function handlePointerUp() {
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerUp);
      document.body.style.removeProperty("cursor");
      document.body.style.removeProperty("user-select");
    }
    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerUp);
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
  }

  function handleResizeKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      setWidth((current) => Math.min(maxPanelWidth(), current + PANEL_RESIZE_STEP));
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      setWidth((current) => Math.max(PANEL_MIN_WIDTH, current - PANEL_RESIZE_STEP));
    }
  }

  return (
    <aside
      role="dialog"
      aria-modal="false"
      aria-label={config.agentName}
      style={{ width: `${width}px` }}
      className={`fixed top-0 right-0 bottom-0 z-46 flex max-w-[96vw] flex-col border-l border-border bg-surface shadow-lg transition-transform duration-300 ease-[cubic-bezier(0.22,0.61,0.36,1)] motion-reduce:transition-none ${
        open ? "translate-x-0" : "translate-x-full"
      }`}
    >
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="Đổi kích thước bảng Trợ lý"
        tabIndex={0}
        onPointerDown={startResizing}
        onKeyDown={handleResizeKeyDown}
        className="absolute top-0 bottom-0 left-0 z-10 w-2 -translate-x-1/2 cursor-col-resize touch-none hover:bg-accent/35 focus-visible:bg-accent/45 focus-visible:outline-none active:bg-accent/50"
      />
      <PanelHeader
        agentInitials={config.agentInitials}
        agentName={config.agentName}
        agentRole={config.agentRole}
        onClose={onClose}
      />
      <PanelTabs active={activeTab} onChange={onTabChange} />

      <div className={`flex min-h-0 flex-1 flex-col ${activeTab === "chat" ? "" : "hidden"}`}>
        <ChatTab
          messages={messages}
          status={status}
          errorMessage={errorMessage}
          sendMessage={sendMessage}
          agentInitials={config.agentInitials}
          userInitials={userInitials}
          active={activeTab === "chat"}
          onOpenReport={openReport}
        />
      </div>

      <ReportTab
        report={report}
        loading={reportLoading}
        notFound={reportNotFound}
        error={reportError}
        loanId={config.loanId}
        active={activeTab === "report"}
      />

      <div className={`flex min-h-0 flex-1 flex-col ${activeTab === "history" ? "" : "hidden"}`}>
        <HistoryTab
          sessions={sessions}
          loading={historyLoading}
          error={historyError}
          activeSessionId={activeThreadId}
          onSelectSession={selectSession}
          onNewSession={newSession}
          onRefresh={refreshHistory}
        />
      </div>
    </aside>
  );
}
