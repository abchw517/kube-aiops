import { PortalApp } from "./app.ts";
import "./styles.css";

const root = document.querySelector<HTMLElement>("#app");
if (!root) throw new Error("#app root element is required");

new PortalApp(root).start();
