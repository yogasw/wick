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

export { default as ProjectMemory } from "./ProjectMemory.svelte";
export { default as PageEditor } from "./PageEditor.svelte";
export { default as Section } from "./Section.svelte";
export { default as BlockedState } from "./BlockedState.svelte";
