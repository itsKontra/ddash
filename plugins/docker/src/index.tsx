import { definePlugin } from "@ddash/plugin";
import { AddProjectPage } from "./add-project";
import { AgentsPage } from "./agents";
import "./index.css";
import { ProjectPage } from "./project";
import { DockerPage } from "./projects";
import { containers, dockerHosts } from "./widget";

export default definePlugin({
  // Label and icon (container) come from plugin.yaml.
  nav: { order: 50 },
  pages: [
    { index: true, Component: DockerPage },
    { path: "agents", Component: AgentsPage },
    { path: "projects/new", Component: AddProjectPage },
    { path: "projects/:host/:name", Component: ProjectPage },
  ],
  widgets: [dockerHosts, containers],
});
