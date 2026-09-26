// @wick-fe/common-agentmemory — the Agent Memory client and the pieces of it
// that more than one surface renders.
//
// Two surfaces read this store, and they answer different questions:
//
//   - the global panel at /tools/agents/agentmemory — the daemon, the whole
//     store, every project's analytics, the settings;
//   - a project's own Agent Memory tab, inside project settings — what THIS
//     project remembers, and the controls to manage it.
//
// Neither is the other with a filter on it, but both talk to the same
// endpoints and render the same page cards, so the client, the types and the
// project view live here rather than in whichever SPA happened to need them
// first (fe-module's deduplication rule).

export * from "./types.js";
export * from "./api.js";
export * from "./format.js";
export * from "./projects.js";
export * from "./projectview.js";
export * from "./stats.js";

export { default as ProjectMemory } from "./ProjectMemory.svelte";
export { default as PageEditor } from "./PageEditor.svelte";
export { default as Section } from "./Section.svelte";
export { default as BlockedState } from "./BlockedState.svelte";

// The scope-agnostic analytics blocks. They take rows and know nothing about
// whose numbers those are, which is what lets the store-wide Analytics tab and
// a project's own tab draw the same shapes without either one borrowing the
// other's figures (stats.ts).
export { default as StatGrid } from "./StatGrid.svelte";
export { default as MeterList } from "./MeterList.svelte";
export { default as Sparkbars } from "./Sparkbars.svelte";
