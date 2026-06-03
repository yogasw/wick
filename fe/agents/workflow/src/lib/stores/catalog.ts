import { writable, get } from "svelte/store";
import { workflowAPI, type CatalogResponse } from "$lib/api/workflow";

// Shared registry catalog — channels + events + connector ops + node
// types. Fetched once per page load and stored here so every
// inspector / palette / trigger modal pulls from the same snapshot
// instead of refetching independently. Backend handler:
// internal/tools/agents/workflows.go workflowRegistryAPI.

export const catalog = writable<CatalogResponse | null>(null);

let loadingPromise: Promise<void> | null = null;

export function loadCatalog(): Promise<void> {
  if (get(catalog)) return Promise.resolve();
  if (loadingPromise) return loadingPromise;
  loadingPromise = workflowAPI
    .catalog()
    .then((res) => {
      catalog.set(res);
    })
    .catch((e) => {
      console.warn("catalog load failed:", e);
      catalog.set(null);
    })
    .finally(() => {
      loadingPromise = null;
    });
  return loadingPromise;
}
