// Re-export: the Agent Memory client and its presentation logic moved to
// @wick-fe/common-agentmemory when the project's own memory tab landed in
// project settings — two surfaces, one implementation (fe-module's
// deduplication rule). This file keeps `$lib/api.js` meaning what it has
// always meant for this SPA's own components and tests.
export * from "@wick-fe/common-agentmemory";
