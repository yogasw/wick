import { mount } from "svelte";
import App from "./App.svelte";
import AgentsApp from "./AgentsApp.svelte";

// One bundle, two shells: the /team page asks for the Agents app with
// data-mode="agents"; every other page gets the session list/detail app.
const appTarget = document.getElementById("app");
if (appTarget) {
  mount(appTarget.dataset.mode === "agents" ? AgentsApp : App, { target: appTarget });
}
