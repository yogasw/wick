import { describe, it, expect } from "vitest";
import { bareSlashCommand } from "../slashCommand.js";

describe("bareSlashCommand", () => {
  it("recognises a command sent on its own", () => {
    expect(bareSlashCommand("/compact")).toBe("compact");
    expect(bareSlashCommand("  /context  ")).toBe("context");
    expect(bareSlashCommand("/agents:list")).toBe("agents:list");
  });

  it("is not a command once there is anything else in the message", () => {
    // "/compact please" is a sentence about compacting, and the backend
    // sends it with the sender line — so it must not render as a command.
    expect(bareSlashCommand("/compact please")).toBe("");
    expect(bareSlashCommand("/compact\nand then commit")).toBe("");
    expect(bareSlashCommand("tolong /compact")).toBe("");
    expect(bareSlashCommand("/")).toBe("");
    expect(bareSlashCommand("")).toBe("");
    expect(bareSlashCommand(undefined)).toBe("");
  });
});
