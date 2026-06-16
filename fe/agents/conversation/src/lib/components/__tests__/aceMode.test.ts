import { describe, test, expect } from "vitest";
import { aceModeFor } from "../../aceMode.js";

describe("aceModeFor", () => {
  test("maps common code extensions to ace modes", () => {
    expect(aceModeFor("src/main.go")).toBe("ace/mode/golang");
    expect(aceModeFor("app.js")).toBe("ace/mode/javascript");
    expect(aceModeFor("mod.mjs")).toBe("ace/mode/javascript");
    expect(aceModeFor("types.ts")).toBe("ace/mode/typescript");
    expect(aceModeFor("App.tsx")).toBe("ace/mode/tsx");
    expect(aceModeFor("widget.jsx")).toBe("ace/mode/jsx");
    expect(aceModeFor("script.py")).toBe("ace/mode/python");
    expect(aceModeFor("model.rb")).toBe("ace/mode/ruby");
    expect(aceModeFor("lib.rs")).toBe("ace/mode/rust");
    expect(aceModeFor("style.css")).toBe("ace/mode/css");
    expect(aceModeFor("style.scss")).toBe("ace/mode/scss");
    expect(aceModeFor("index.html")).toBe("ace/mode/html");
    expect(aceModeFor("data.xml")).toBe("ace/mode/xml");
    expect(aceModeFor("config.yaml")).toBe("ace/mode/yaml");
    expect(aceModeFor("config.yml")).toBe("ace/mode/yaml");
    expect(aceModeFor("Cargo.toml")).toBe("ace/mode/toml");
    expect(aceModeFor("readme.md")).toBe("ace/mode/markdown");
    expect(aceModeFor("readme.markdown")).toBe("ace/mode/markdown");
    expect(aceModeFor("run.sh")).toBe("ace/mode/sh");
    expect(aceModeFor("dump.sql")).toBe("ace/mode/sql");
    expect(aceModeFor("Main.java")).toBe("ace/mode/java");
    expect(aceModeFor("index.php")).toBe("ace/mode/php");
    expect(aceModeFor("data.json")).toBe("ace/mode/json");
  });

  test("maps Dockerfile-related extensions", () => {
    expect(aceModeFor("service.dockerfile")).toBe("ace/mode/dockerfile");
  });

  test("falls back to text mode for unknown or extensionless files", () => {
    expect(aceModeFor("notes.txt")).toBe("ace/mode/text");
    expect(aceModeFor("LICENSE")).toBe("ace/mode/text");
    expect(aceModeFor("data.unknownext")).toBe("ace/mode/text");
  });

  test("is case-insensitive on the extension", () => {
    expect(aceModeFor("MAIN.GO")).toBe("ace/mode/golang");
    expect(aceModeFor("Index.HTML")).toBe("ace/mode/html");
  });
});
