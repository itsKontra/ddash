import { createQueryClient } from "@ddash/plugin";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { App } from "./app";
import { applyScheme } from "./appearance";
import "./index.css";

// Before the first paint; the shell keeps it up to date (useAppearance).
applyScheme();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <App client={createQueryClient()} />
    </BrowserRouter>
  </StrictMode>,
);
