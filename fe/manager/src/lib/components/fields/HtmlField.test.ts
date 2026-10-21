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

// findEl waits until the widget has rendered the element (waitFor only
// retries on a throw, so a bare querySelector null would pass at once).
async function findEl<T extends Element>(sel: string): Promise<T> {
  return waitFor(() => {
    const el = document.querySelector<T>(sel);
    if (!el) throw new Error("not rendered yet: " + sel);
    return el;
  });
}

test("a hidden input follows the new markup after an action", async () => {
  // The add op answers with the posted count + 1. If the morph kept the old
  // live value of the hidden input, the second click would post 2 again.
  const markup = (n: number) => `<input type="hidden" name="row_count" value="${n}"/><button data-op="add">Add ${n}</button>`;
  const spy = vi.spyOn(api, "runConnectorTest").mockImplementation((_k, _id, operation, input) => {
    const n = operation === "add" ? Number((input as Record<string, string>).row_count) + 1 : 2;
    return Promise.resolve({ error: "", response: { html: markup(n) } } as never);
  });
  render(HtmlField, {
    props: { connectorKey: "k", connectorId: "id1", op: "render", value: "", onChange: vi.fn() },
  });
  await fireEvent.click(await screen.findByText("Add 2"));
  await fireEvent.click(await screen.findByText("Add 3"));
  await screen.findByText("Add 4");
  const adds = spy.mock.calls.filter((c) => c[2] === "add").map((c) => (c[3] as Record<string, string>).row_count);
  expect(adds).toEqual(["2", "3"]);
});

test("a typed visible input keeps the operator's text across a render", async () => {
  mockRun([
    { html: `<input name="q" value=""/><button data-op="go">Go</button>` },
    { html: `<input name="q" value=""/><p>done</p><button data-op="go">Go</button>` },
  ]);
  render(HtmlField, {
    props: { connectorKey: "k", connectorId: "id1", op: "render", value: "", onChange: vi.fn() },
  });
  const q = await findEl<HTMLInputElement>('[name="q"]');
  await fireEvent.input(q, { target: { value: "half typed" } });
  await fireEvent.click(screen.getByText("Go"));
  await screen.findByText("done");
  expect((document.querySelector('[name="q"]') as HTMLInputElement).value).toBe("half typed");
});

test("data-op-input runs its op once, debounced, with the typed value", async () => {
  const spy = mockByOp({
    render: { html: `<input name="q_0" data-op-input="search" data-arg="0" value=""/>` },
    search: { html: `<input name="q_0" data-op-input="search" data-arg="0" value=""/><p>3 matches</p>` },
  });
  render(HtmlField, {
    props: { connectorKey: "k", connectorId: "id1", op: "render", value: "", onChange: vi.fn() },
  });
  const q = await findEl<HTMLInputElement>('[name="q_0"]');
  await fireEvent.input(q, { target: { value: "t" } });
  await fireEvent.input(q, { target: { value: "ta" } });
  await fireEvent.input(q, { target: { value: "tau" } });
  await screen.findByText("3 matches", {}, { timeout: 2000 });
  const searches = spy.mock.calls.filter((c) => c[2] === "search");
  expect(searches).toHaveLength(1);
  expect(searches[0][3]).toMatchObject({ browser: "0", q_0: "tau" });
  expect((document.querySelector('[name="q_0"]') as HTMLInputElement).value).toBe("tau");
});
