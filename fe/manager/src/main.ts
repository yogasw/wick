import { mount } from "svelte";
import App from "./App.svelte";
import PluginsEmbed from "$lib/components/plugins/PluginsEmbed.svelte";

const target = document.getElementById("app");
/* /admin/plugins hosts this bundle inside the admin chrome and asks for the
   plugins view alone: no manager navbar, no breadcrumbs. */
if (target?.dataset.embed === "plugins") mount(PluginsEmbed, { target });
else if (target) mount(App, { target });
