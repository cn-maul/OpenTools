import "@fontsource-variable/instrument-sans";
import "@fontsource-variable/jetbrains-mono";
import "@fontsource-variable/noto-sans-sc";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import App from "./App";
import "./styles.css";

const container = document.getElementById("app");
if (!container) throw new Error("OpenTools: missing #app mount point");

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
