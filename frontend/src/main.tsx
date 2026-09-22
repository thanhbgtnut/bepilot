import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import "./index.css";
import { mountAppraisalAgentWidget } from "./mount.tsx";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

// Exercises the exact same mount path a real host page would use — a
// separate container, isolated in its own Shadow DOM, appended to <body>.
mountAppraisalAgentWidget();
