import { mount } from "svelte";
import App from "./App.svelte";

// The admin page renders an empty div and tells us where the data lives, so
// the endpoint is never hard-coded in two places.
const target = document.getElementById("wick-analytics");
if (target) {
  mount(App, {
    target,
    props: { endpoint: target.dataset.endpoint ?? "/admin/analytics/users.json" },
  });
}
