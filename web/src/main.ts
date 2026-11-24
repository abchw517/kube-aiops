import { PortalApp } from "./app.ts";
import "./styles.css";
import "./components/portal-shell.css";
import "./views/diagnosis/diagnosis.css";
import "./views/diagnosis/unified-overview.css";
import "./views/permissions/audit.css";
import "./views/permissions/authentication.css";
import "./views/permissions/permission-workspace.css";
import "./views/resources/resources.css";
import "./views/wiki/wiki.css";

const root = document.querySelector<HTMLElement>("#app");
if (!root) throw new Error("#app root element is required");

new PortalApp(root).start();
