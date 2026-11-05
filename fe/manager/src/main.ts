import { mount } from "svelte";
import App from "./App.svelte";
import PluginsEmbed from "$lib/components/plugins/PluginsEmbed.svelte";
import AuditEmbed from "$lib/components/audit/AuditEmbed.svelte";

const target = document.getElementById("app");
/* /admin/plugins hosts this bundle inside the admin chrome and asks for the
   plugins view alone: no manager navbar, no breadcrumbs. */
if (target?.dataset.embed === "plugins") mount(PluginsEmbed, { target });
/* /admin/audit does the same for the audit views. */
else if (target?.dataset.embed === "audit") mount(AuditEmbed, { target });
else if (target) mount(App, { target });
