import { PortalApp } from "./app.ts";
import "./components/theme.css";
import "./styles.css";
import "./components/portal-shell.css";
import "./views/diagnosis/diagnosis.css";
import "./views/diagnosis/unified-overview.css";
import "./views/permissions/audit.css";
import "./views/permissions/authentication.css";
import "./views/permissions/permission-workspace.css";
import "./views/resources/resources.css";
import "./views/resources/catalog.css";
import "./views/wiki/wiki.css";
import "./views/ai/ai.css";
import "./views/ai/workspace.css";
import "./views/diagnosis/recent-workspace.css";

const root = document.querySelector<HTMLElement>("#app");
if (!root) throw new Error("#app root element is required");

new PortalApp(root).start();
