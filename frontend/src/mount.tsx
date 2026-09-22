import { createRoot } from "react-dom/client";
import { AgentDock } from "./widget/AgentDock";
import type { WidgetConfig } from "./widget/types";
import widgetStyles from "./widget/styles.css?inline";

const DEFAULT_CONFIG: WidgetConfig = {
  agentName: "Trợ lý Thẩm định",
  agentInitials: "TĐ",
  agentRole: "Vai trò: Chuyên viên thẩm định DN · Bậc Junior 3/5 · 1.248 hồ sơ",
  userInitials: "LH",
  loanId: "LOS-2026-04871",
  borrowerName: "Công ty CP Thực phẩm Đồng Xanh",
  // AG-UI backend tác tử bepilot (`make run-web` → cổng 9090, route
  // /v1/ag-ui/run). Đổi qua data-agent-url / mount options / biến môi trường
  // VITE_AGENT_URL; mock cũ: http://localhost:8787/agent
  agentUrl:
    (import.meta.env.VITE_AGENT_URL as string | undefined) ??
    "http://localhost:9090/v1/ag-ui/run",
  pendingCount: 0,
  autoOpenDelay: 650,
  nudgeDelay: 12000,
};

export interface MountOptions extends Partial<WidgetConfig> {
  /** Element (or CSS selector) the widget's isolated container mounts into. Defaults to document.body. */
  target?: HTMLElement | string;
}

export interface MountedWidget {
  unmount: () => void;
}

/**
 * Mounts the widget into a Shadow DOM root so its styles never leak into
 * (or clash with) the host page, and vice versa.
 */
export function mountAppraisalAgentWidget(
  options: MountOptions = {},
): MountedWidget {
  const { target, ...configOverrides } = options;
  const config: WidgetConfig = { ...DEFAULT_CONFIG, ...configOverrides };

  const parent =
    (typeof target === "string" ? document.querySelector(target) : target) ??
    document.body;

  if (!parent) {
    throw new Error("appraisal-agent-widget: mount target not found");
  }

  const host = document.createElement("div");
  parent.appendChild(host);

  const shadowRoot = host.attachShadow({ mode: "open" });

  const style = document.createElement("style");
  style.textContent = widgetStyles;
  shadowRoot.appendChild(style);

  const mountPoint = document.createElement("div");
  shadowRoot.appendChild(mountPoint);

  const root = createRoot(mountPoint);
  root.render(<AgentDock config={config} />);

  return {
    unmount: () => {
      root.unmount();
      host.remove();
    },
  };
}
