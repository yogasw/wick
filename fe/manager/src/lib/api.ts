import { apiGet } from "@wick-fe/common-api";
import type { ConnectorDef } from "./types.js";

/* Connector definitions live at the server-absolute /manager surface,
   distinct from the SPA mount base (/modules/manager/app). */
export async function listConnectors(): Promise<ConnectorDef[]> {
  const r = await apiGet<ConnectorDef[] | null>("/manager/api/connectors");
  return (r ?? []).map((c) => ({
    key: c.key,
    name: c.name,
    category: c.category ?? "",
    icon: c.icon ?? "",
    custom: c.custom ?? false,
    disabled: c.disabled ?? false,
  }));
}
