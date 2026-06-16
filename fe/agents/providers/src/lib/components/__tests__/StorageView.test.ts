import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import StorageView from "../StorageView.svelte";
import * as api from "$lib/api.js";

vi.mock("$lib/api.js");
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

function makeStorage() {
  return {
    files: [
      {
        id: 1,
        provider_type: "claude",
        instance_name: "default",
        rel_path: "config.json",
        name: "config.json",
        is_dir: false,
        size: 1024,
        synced_at: "2024-01-01T00:00:00Z",
        retention_days: 7,
      },
      {
        id: 2,
        provider_type: "openai",
        instance_name: "gpt4",
        rel_path: "settings.json",
        name: "settings.json",
        is_dir: false,
        size: 512,
        synced_at: "2024-01-02T00:00:00Z",
        retention_days: 0,
      },
    ],
    filter_provider: "",
    filter_instance: "",
    provider_types: ["claude", "openai"],
  };
}

beforeEach(() => {
  vi.mocked(api.apiGetStorage).mockResolvedValue(makeStorage());
  vi.mocked(api.apiStorageRetention).mockResolvedValue(undefined);
  vi.mocked(api.apiStoragePreview).mockResolvedValue({ rel_path: "config.json", size: 1024, is_binary: false, content: '{"key":"value"}' });
  vi.mocked(api.apiStorageRestore).mockResolvedValue({ restored: 1 });
  vi.mocked(api.apiStorageDelete).mockResolvedValue(undefined);
  vi.mocked(api.apiStorageSync).mockResolvedValue(undefined);
});

describe("StorageView", () => {
  it("renders table with file rows after load", async () => {
    render(StorageView, { props: { onBack: vi.fn() } });
    expect(await screen.findByText("config.json")).toBeTruthy();
    expect(screen.getByText("settings.json")).toBeTruthy();
  });

  it("shows provider/instance in table", async () => {
    render(StorageView, { props: { onBack: vi.fn() } });
    await screen.findByText("config.json");
    const claudeCells = screen.getAllByText("claude");
    expect(claudeCells.length).toBeGreaterThan(0);
  });

  it("calls onBack when back button clicked", async () => {
    const onBack = vi.fn();
    render(StorageView, { props: { onBack } });
    await screen.findByText("config.json");
    fireEvent.click(screen.getByRole("button", { name: "Providers" }));
    expect(onBack).toHaveBeenCalled();
  });

  it("calls apiStoragePreview when Preview clicked", async () => {
    render(StorageView, { props: { onBack: vi.fn() } });
    await screen.findByText("config.json");
    const btns = screen.getAllByText("Preview");
    fireEvent.click(btns[0]);
    expect(vi.mocked(api.apiStoragePreview)).toHaveBeenCalledWith(1);
  });

  it("shows preview modal with content", async () => {
    render(StorageView, { props: { onBack: vi.fn() } });
    await screen.findByText("config.json");
    const btns = screen.getAllByText("Preview");
    fireEvent.click(btns[0]);
    expect(await screen.findByText('{"key":"value"}')).toBeTruthy();
  });

  it("shows delete buttons for each file row", async () => {
    render(StorageView, { props: { onBack: vi.fn() } });
    await screen.findByText("config.json");
    const deleteBtns = screen.getAllByText("Delete");
    expect(deleteBtns.length).toBeGreaterThanOrEqual(2);
  });

  it("calls apiStorageRestore when Restore Selected clicked", async () => {
    render(StorageView, { props: { onBack: vi.fn() } });
    await screen.findByText("config.json");
    const checkboxes = document.querySelectorAll("input[type=checkbox]");
    const fileCheckbox = checkboxes[1] as HTMLInputElement;
    fireEvent.click(fileCheckbox);
    const restoreBtn = await screen.findByText("Restore Selected");
    fireEvent.click(restoreBtn);
    expect(vi.mocked(api.apiStorageRestore)).toHaveBeenCalled();
  });

  it("shows empty state when no files", async () => {
    vi.mocked(api.apiGetStorage).mockResolvedValue({ ...makeStorage(), files: [] });
    render(StorageView, { props: { onBack: vi.fn() } });
    expect(await screen.findByText(/No storage files found/)).toBeTruthy();
  });

  it("shows error state on API failure", async () => {
    vi.mocked(api.apiGetStorage).mockRejectedValue(new Error("storage unavailable"));
    render(StorageView, { props: { onBack: vi.fn() } });
    expect(await screen.findByText("storage unavailable")).toBeTruthy();
  });

  it("renders upload panel when Upload Backup clicked", async () => {
    render(StorageView, { props: { onBack: vi.fn() } });
    await screen.findByText("config.json");
    fireEvent.click(screen.getByText("Upload Backup"));
    expect(screen.getByText("Upload Backup File")).toBeTruthy();
  });
});
