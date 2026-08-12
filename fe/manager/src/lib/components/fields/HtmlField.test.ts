import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { vi, test, expect, beforeEach } from "vitest";
import HtmlField from "./HtmlField.svelte";
import * as api from "$lib/api.js";

// Each test stubs runConnectorTest to return a scripted op response, then drives
// the widget's button and asserts the core applied fields / rendered feedback.
beforeEach(() => {
  vi.restoreAllMocks();
});

function mockRun(responses: Array<Record<string, unknown>>) {
  const spy = vi.spyOn(api, "runConnectorTest");
  for (const r of responses) {
    spy.mockResolvedValueOnce({ error: "", response: r } as never);
  }
  return spy;
}

// mockByOp returns a response chosen by the operation name, so extra fetches
// (the value $effect can fetch twice) don't desync a sequential queue.
function mockByOp(byOp: Record<string, Record<string, unknown>>) {
  return vi
    .spyOn(api, "runConnectorTest")
    .mockImplementation((_k, _id, operation) =>
      Promise.resolve({ error: "", response: byOp[operation] ?? {} } as never),
    );
}

test("op returning { fields } calls onSetFields with the map", async () => {
  // First call = initial fetchHtml (renders a form with a button + textarea).
  // Second call = the button op, returning fields to apply.
  mockRun([
    { html: `<textarea name="raw"></textarea><button data-op="extract">Extract</button>` },
    { fields: { token_v2: "v03:abc", user_agent: "UA1" }, html: `<p>✓ extracted 2 fields</p>` },
  ]);
  const onSetFields = vi.fn();

  render(HtmlField, {
    props: {
      connectorKey: "notion_unofficial",
      connectorId: "id1",
      op: "import_status",
      value: "",
      onChange: vi.fn(),
      onSetFields,
    },
  });

  const btn = await screen.findByText("Extract");
  await fireEvent.click(btn);

  await waitFor(() => expect(onSetFields).toHaveBeenCalledOnce());
  expect(onSetFields).toHaveBeenCalledWith({ token_v2: "v03:abc", user_agent: "UA1" });
});

test("op returning { html } replaces the markup (connector feedback)", async () => {
  mockRun([
    { html: `<button data-op="validate">Check</button>` },
    { html: `<p>❌ token invalid</p>` },
  ]);

  render(HtmlField, {
    props: {
      connectorKey: "x",
      connectorId: "id1",
      op: "status",
      value: "",
      onChange: vi.fn(),
      onSetFields: vi.fn(),
    },
  });

  await fireEvent.click(await screen.findByText("Check"));
  await waitFor(() => expect(screen.getByText("❌ token invalid")).toBeTruthy());
});

test("named inputs in the connector HTML are sent to the op", async () => {
  const spy = mockByOp({
    status: { html: `<textarea name="raw"></textarea><button data-op="extract">Go</button>` },
    extract: { html: `<p>done</p>` },
  });

  const { container } = render(HtmlField, {
    props: {
      connectorKey: "x",
      connectorId: "id1",
      op: "status",
      value: "",
      onChange: vi.fn(),
      onSetFields: vi.fn(),
    },
  });

  // Wait for the connector form to be injected via {@html}, grab both the
  // textarea and its button together, type, then click.
  const { ta, btn } = await waitFor(() => {
    const t = container.querySelector<HTMLTextAreaElement>("textarea[name='raw']");
    const b = container.querySelector<HTMLButtonElement>("button[data-op='extract']");
    if (!t || !b) throw new Error("form not rendered yet");
    return { ta: t, btn: b };
  });
  await fireEvent.input(ta, { target: { value: "curl ..." } });
  await fireEvent.click(btn);

  // Find the call for the button op ("extract"); its input must carry the
  // textarea value under `raw`.
  await waitFor(() => {
    const call = spy.mock.calls.find((c) => c[2] === "extract");
    expect(call?.[3]).toMatchObject({ raw: "curl ..." });
  });
});

// A checkbox carries its state in `checked`, not in `value` — `value` is the
// static attribute the connector wrote in the markup and reads "true" whether or
// not the box is ticked. Collecting `value` therefore turned every unchecked box
// into an affirmative submission: the git connector's policy editor renders
// <input type="checkbox" name="g_force" value="true">, and its save handler treats
// any non-empty g_force as "allowed", so allow_force_push re-enabled itself on
// every save no matter what the operator did with the box.
test("an UNCHECKED checkbox is omitted from the op input", async () => {
  const spy = mockByOp({
    status: {
      html:
        `<input type="checkbox" name="g_force" value="true"/>` +
        `<button data-op="save">Save</button>`,
    },
    save: { html: `<p>saved</p>` },
  });

  const { container } = render(HtmlField, {
    props: {
      connectorKey: "x",
      connectorId: "1",
      op: "status",
      value: "",
      onChange: vi.fn(),
      onSetFields: vi.fn(),
    },
  });

  const btn = await waitFor(() => {
    const b = container.querySelector<HTMLButtonElement>("button[data-op='save']");
    if (!b) throw new Error("form not rendered yet");
    return b;
  });
  await fireEvent.click(btn);

  await waitFor(() => {
    const call = spy.mock.calls.find((c) => c[2] === "save");
    expect(call).toBeTruthy();
    expect(call?.[3]).not.toHaveProperty("g_force");
  });
});

test("a CHECKED checkbox is sent with its value attribute", async () => {
  const spy = mockByOp({
    status: {
      html:
        `<input type="checkbox" name="g_force" value="true" checked/>` +
        `<button data-op="save">Save</button>`,
    },
    save: { html: `<p>saved</p>` },
  });

  const { container } = render(HtmlField, {
    props: {
      connectorKey: "x",
      connectorId: "1",
      op: "status",
      value: "",
      onChange: vi.fn(),
      onSetFields: vi.fn(),
    },
  });

  const btn = await waitFor(() => {
    const b = container.querySelector<HTMLButtonElement>("button[data-op='save']");
    if (!b) throw new Error("form not rendered yet");
    return b;
  });
  await fireEvent.click(btn);

  await waitFor(() => {
    const call = spy.mock.calls.find((c) => c[2] === "save");
    expect(call?.[3]).toMatchObject({ g_force: "true" });
  });
});

// A radio group submits only the selected member, under the shared name.
test("only the SELECTED radio in a group is sent", async () => {
  const spy = mockByOp({
    status: {
      html:
        `<input type="radio" name="mode" value="soft"/>` +
        `<input type="radio" name="mode" value="hard" checked/>` +
        `<button data-op="save">Save</button>`,
    },
    save: { html: `<p>saved</p>` },
  });

  const { container } = render(HtmlField, {
    props: {
      connectorKey: "x",
      connectorId: "1",
      op: "status",
      value: "",
      onChange: vi.fn(),
      onSetFields: vi.fn(),
    },
  });

  const btn = await waitFor(() => {
    const b = container.querySelector<HTMLButtonElement>("button[data-op='save']");
    if (!b) throw new Error("form not rendered yet");
    return b;
  });
  await fireEvent.click(btn);

  await waitFor(() => {
    const call = spy.mock.calls.find((c) => c[2] === "save");
    expect(call?.[3]).toMatchObject({ mode: "hard" });
  });
});
