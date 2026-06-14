import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ConfirmDialog from "../ConfirmDialog.svelte";

describe("ConfirmDialog", () => {
  test("does not render when open=false", () => {
    render(ConfirmDialog, {
      props: {
        open: false,
        title: "Delete?",
        onConfirm: vi.fn(),
        onCancel: vi.fn(),
      },
    });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  test("renders title when open=true", () => {
    render(ConfirmDialog, {
      props: {
        open: true,
        title: "Delete item?",
        onConfirm: vi.fn(),
        onCancel: vi.fn(),
      },
    });
    expect(screen.getByRole("dialog")).toBeDefined();
    expect(screen.getByText("Delete item?")).toBeDefined();
  });

  test("calls onConfirm when confirm button clicked", async () => {
    const onConfirm = vi.fn();
    render(ConfirmDialog, {
      props: {
        open: true,
        title: "Sure?",
        confirmLabel: "Yes",
        onConfirm,
        onCancel: vi.fn(),
      },
    });
    await fireEvent.click(screen.getByText("Yes"));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  test("calls onCancel when cancel button clicked", async () => {
    const onCancel = vi.fn();
    render(ConfirmDialog, {
      props: {
        open: true,
        title: "Sure?",
        onConfirm: vi.fn(),
        onCancel,
      },
    });
    await fireEvent.click(screen.getByText("Cancel"));
    expect(onCancel).toHaveBeenCalledOnce();
  });
});
