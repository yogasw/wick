import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import RemoteSourcePicker from "../RemoteSourcePicker.svelte";

describe("RemoteSourcePicker", () => {
  test("A2A, Slack and Plugin pick; HTTP is disabled, coming later", async () => {
    const onSource = vi.fn();
    render(RemoteSourcePicker, { value: "a2a", onSource });
    const radios = screen.getAllByRole("radio");
    expect(radios.map((r) => r.querySelector("span")?.textContent)).toEqual(["A2A", "Slack", "HTTP", "Plugin"]);
    expect(screen.getByTestId("remote-source-a2a").getAttribute("aria-checked")).toBe("true");
    for (const k of ["http"]) {
      const b = screen.getByTestId(`remote-source-${k}`) as HTMLButtonElement;
      expect(b.disabled).toBe(true);
      expect(b.textContent).toContain("Coming later");
    }
    await fireEvent.click(screen.getByTestId("remote-source-http"));
    await fireEvent.click(screen.getByTestId("remote-source-a2a"));
    expect(onSource).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByTestId("remote-source-slack"));
    expect(onSource).toHaveBeenCalledWith("slack");
    await fireEvent.click(screen.getByTestId("remote-source-plugin"));
    expect(onSource).toHaveBeenCalledWith("plugin");
  });
});
