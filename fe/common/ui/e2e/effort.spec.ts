import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const source = (name: string) => readFileSync(fileURLToPath(new URL("../src/" + name, import.meta.url)), "utf8");
const helpers = source("effort-ui.mjs").replaceAll("export function", "function").replace("const NEUTRAL", "var NEUTRAL").replace("const TONES", "var TONES");
const picker = source("effort-picker.mjs")
  .replace("import { effortAngle, effortFraction, effortIndex, effortTone } from './effort-ui.mjs';", "")
  .replaceAll("export function", "function");

const fixture = `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{background:#111;color:white;font:16px system-ui}header{text-align:center;padding:16px}#picker{position:fixed;bottom:30px;right:50px;--effort-bottom:120px}</style>
<header>Wick</header><span id="picker"></span>
<script>${helpers}\n${picker}
window.changes=[];
window.control=mountEffortPicker(document.querySelector('#picker'),{
 options:['default','low','medium','high','xhigh'].map((id,i)=>({id,label:['Default','Low','Medium','High','Extra High'][i]})),
 value:'high',onChange:id=>window.changes.push(id)
});
</script>`;

test("mobile effort previews during drag and commits once, follows model changes", async ({ page }) => {
  await page.setViewportSize({ width:390,height:844 });
  await page.setContent(fixture);
  const trigger=page.locator("#picker .trigger"), slider=page.locator("#picker input");
  const label=page.locator("#picker .label span"), needle=page.locator("#picker .needle");
  await trigger.click();
  await expect(label).toHaveText("High");
  await slider.focus();
  await slider.press("End");
  await expect(label).toHaveText("Extra High");
  await expect(needle).toHaveAttribute("style", /rotate\(120deg\)/);
  const changes=()=>page.evaluate(()=> (window as unknown as {changes:string[]}).changes);
  expect(await changes()).toEqual(["xhigh"]);
  const box=(await slider.boundingBox())!;
  await page.mouse.move(box.x+box.width-18,box.y+box.height/2);
  await page.mouse.down();
  await page.mouse.move(box.x+18,box.y+box.height/2,{steps:8});
  await expect(label).toHaveText("Default");
  expect(await changes()).toEqual(["xhigh"]);
  await page.mouse.up();
  expect(await changes()).toEqual(["xhigh","default"]);
  await page.keyboard.press("Escape");
  await expect(slider).toBeHidden();
  await page.evaluate(()=>{
    const w=window as unknown as {control:{update:(x:unknown)=>void};changes:string[]};
    w.control.update({options:[{id:"default",label:"Default"},{id:"low",label:"Low"}],value:"xhigh",onChange:(id:string)=>w.changes.push(id)});
  });
  await trigger.click();
  await expect(label).toHaveText("Default");
  await expect(slider).toHaveAttribute("max","1");
  await page.locator("header").click();
  await expect(slider).toBeHidden();
});

test("desktop keyboard and dismissal", async ({page})=>{
  await page.setViewportSize({width:1100,height:760});
  await page.setContent(fixture);
  const trigger=page.locator("#picker .trigger"), slider=page.locator("#picker input");
  await trigger.click();
  await slider.press("Home");
  await expect(page.locator("#picker .label span")).toHaveText("Default");
  await slider.press("ArrowRight");
  await expect(page.locator("#picker .label span")).toHaveText("Low");
  // the parent re-renders with the committed value: the panel stays open
  await page.evaluate(()=>{
    const w=window as unknown as {control:{update:(x:unknown)=>void};changes:string[]};
    w.control.update({options:['default','low','medium','high','xhigh'].map((id,i)=>({id,label:['Default','Low','Medium','High','Extra High'][i]})),value:"low",onChange:(id:string)=>w.changes.push(id)});
  });
  await expect(slider).toBeVisible();
  await slider.press("ArrowRight");
  await expect(page.locator("#picker .label span")).toHaveText("Medium");
  await page.keyboard.press("Escape");
  await expect(trigger).toHaveAttribute("aria-expanded","false");
});
