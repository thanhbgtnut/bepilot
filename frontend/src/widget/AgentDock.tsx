import { useEffect, useState } from "react";
import { Callout } from "./Callout";
import { Fab } from "./Fab";
import { Panel } from "./panel/Panel";
import type { PanelTabId } from "./panel/PanelTabs";
import { Scrim } from "./Scrim";
import type { WidgetConfig } from "./types";

interface AgentDockProps {
  config: WidgetConfig;
}

export function AgentDock({ config }: AgentDockProps) {
  const [open, setOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<PanelTabId>("chat");
  const [calloutVisible, setCalloutVisible] = useState(false);

  function openDock(tab: PanelTabId = "chat") {
    setOpen(true);
    setActiveTab(tab);
    setCalloutVisible(false);
  }
  function closeDock() {
    setOpen(false);
  }

  useEffect(() => {
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    const timers: number[] = [];
    if (config.autoOpenDelay > 0) {
      timers.push(
        window.setTimeout(() => openDock("chat"), reduce ? 0 : config.autoOpenDelay),
      );
    }
    if (config.nudgeDelay > 0) {
      timers.push(
        window.setTimeout(
          () => {
            setOpen((currentlyOpen) => {
              if (!currentlyOpen) setCalloutVisible(true);
              return currentlyOpen;
            });
          },
          reduce ? 400 : config.nudgeDelay,
        ),
      );
    }
    return () => timers.forEach((timer) => window.clearTimeout(timer));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    function handleKeydown(event: KeyboardEvent) {
      if (event.key === "Escape" && open) closeDock();
    }
    document.addEventListener("keydown", handleKeydown);
    return () => document.removeEventListener("keydown", handleKeydown);
  }, [open]);

  return (
    <div className="aaw-root">
      <Scrim show={open} onClick={closeDock} />

      {!open && (
        <Fab
          agentInitials={config.agentInitials}
          agentName={config.agentName}
          pendingCount={config.pendingCount}
          onClick={() => openDock("chat")}
        />
      )}

      {!open && calloutVisible && (
        <Callout
          agentInitials={config.agentInitials}
          agentName={config.agentName}
          loanId={config.loanId}
          onOpen={() => openDock("chat")}
          onDismiss={() => setCalloutVisible(false)}
        />
      )}

      <Panel
        config={config}
        userInitials={config.userInitials}
        open={open}
        activeTab={activeTab}
        onTabChange={setActiveTab}
        onClose={closeDock}
      />
    </div>
  );
}
