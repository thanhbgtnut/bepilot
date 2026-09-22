import { mountAppraisalAgentWidget, type MountOptions } from "../mount";

// Captured synchronously while this classic <script> is executing — it is
// no longer available once we defer into a DOMContentLoaded callback.
const scriptEl = document.currentScript as HTMLScriptElement | null;

function readConfig(): MountOptions {
  const dataset = scriptEl?.dataset ?? {};
  const config: MountOptions = {};

  if (dataset.target) config.target = dataset.target;
  if (dataset.agentName) config.agentName = dataset.agentName;
  if (dataset.agentInitials) config.agentInitials = dataset.agentInitials;
  if (dataset.agentRole) config.agentRole = dataset.agentRole;
  if (dataset.userInitials) config.userInitials = dataset.userInitials;
  if (dataset.loanId) config.loanId = dataset.loanId;
  if (dataset.borrowerName) config.borrowerName = dataset.borrowerName;
  if (dataset.agentUrl) config.agentUrl = dataset.agentUrl;
  if (dataset.agentAuthToken) {
    config.agentHeaders = { Authorization: `Bearer ${dataset.agentAuthToken}` };
  }
  if (dataset.pendingCount) config.pendingCount = Number(dataset.pendingCount);
  if (dataset.autoOpenDelay) config.autoOpenDelay = Number(dataset.autoOpenDelay);
  if (dataset.nudgeDelay) config.nudgeDelay = Number(dataset.nudgeDelay);

  return config;
}

function init() {
  mountAppraisalAgentWidget(readConfig());
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init);
} else {
  init();
}
