import { writable } from "svelte/store";

/* Holds display names for the current page so the breadcrumb in App.svelte
   can show human labels (connector / row / job / tool names) instead of raw
   URL keys. Page components publish into this when their data loads; the
   route key falls back when a name has not arrived yet. Cleared on unmount
   so a stale name never leaks into the next page's trail. */
export interface BreadcrumbNames {
  connector?: string;
  row?: string;
  job?: string;
  tool?: string;
}

export const breadcrumbNames = writable<BreadcrumbNames>({});

export function setBreadcrumbNames(names: BreadcrumbNames): void {
  breadcrumbNames.set(names);
}

export function clearBreadcrumbNames(): void {
  breadcrumbNames.set({});
}
