import { writable } from "svelte/store";

export const NOTIFY_KEY = "wick.conv.notify";

function readPref(): boolean {
  try {
    return localStorage.getItem(NOTIFY_KEY) === "true";
  } catch (_) {
    return false;
  }
}

function writePref(val: boolean): void {
  try {
    localStorage.setItem(NOTIFY_KEY, val ? "true" : "false");
  } catch (_) { /* storage blocked */ }
}

export const notifyEnabled = writable<boolean>(readPref());

notifyEnabled.subscribe((val) => writePref(val));
