import { mount } from "svelte";
import App from "./App.svelte";

const appTarget = document.getElementById("app");
if (appTarget) mount(App, { target: appTarget });
