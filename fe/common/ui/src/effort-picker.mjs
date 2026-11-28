import { effortAngle, effortFraction, effortIndex, effortTone } from './effort-ui.mjs';

// Gauge: 240deg arc centred on (12,14), r=9 — needle -120..120deg matches the arc ends.
const ARC = 'M4.21 18.5A9 9 0 1 1 19.79 18.5';
function gaugeSvg(angle, f, tone) {
  const dash = f === null ? 0 : Math.max(2, f * 100);
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="rgb(${tone})" stroke-width="2.4" stroke-linecap="round"><path d="${ARC}" pathLength="100" opacity=".25"/><path d="${ARC}" pathLength="100" stroke-dasharray="${dash} 100"/><g transform="rotate(${angle} 12 14)"><path d="M12 14V7.5" stroke-width="2"/><circle cx="12" cy="14" r="2" fill="rgb(${tone})" stroke="none"/></g></svg>`;
}
const gaugeDataUrl = (angle, f, tone) => 'data:image/svg+xml,' + encodeURIComponent(gaugeSvg(angle, f, tone));

// The caller owns persistence and capabilities. This component never invents options.
export function mountEffortPicker(host, initial) {
  const root = host.shadowRoot || host.attachShadow({mode:'open'});
  root.innerHTML = `<style>
  :host{display:inline-flex;position:relative;color:inherit;font:inherit}
  button{font:inherit;cursor:pointer}
  .trigger{--tone:39 177 153;display:inline-grid;place-items:center;box-sizing:border-box;width:32px;height:32px;padding:0;border-radius:8px;border:1px solid rgb(var(--tone)/.45);background:rgb(var(--tone)/.12);color:rgb(var(--tone));transition:background-color 150ms,border-color 150ms,color 150ms}
  .trigger:hover{background:rgb(var(--tone)/.22)}
  .trigger:focus-visible{outline:2px solid rgb(var(--tone));outline-offset:2px}
  .arc-bg{opacity:.28}.arc{transition:stroke-dasharray 140ms ease-out}
  .trigger:disabled{opacity:.5;cursor:default}
  svg{width:22px;height:22px}.needle{transform-origin:12px 14px;transition:transform 140ms ease-out}
  .panel{position:absolute;bottom:calc(100% + 8px);right:0;width:288px;max-width:calc(100vw - 24px);box-sizing:border-box;padding:12px 14px 10px;border-radius:12px;border:1px solid var(--ep-border,#e5e7eb);background:var(--ep-panel,#fff);color:var(--ep-text,#111);box-shadow:0 10px 15px -3px rgb(0 0 0/.1),0 4px 6px -4px rgb(0 0 0/.1);z-index:30;text-align:left}
  .panel[hidden]{display:none}
  .label{display:flex;align-items:baseline;justify-content:space-between;gap:8px;margin:0 0 10px;font-size:12px;line-height:1.3}
  .label small{font-size:12px;color:var(--ep-muted,#6b7280)}
  .label span{font-size:14px;font-weight:600;color:rgb(var(--tone,39 177 153));transition:color 140ms}
  .track{position:relative;box-sizing:border-box;height:32px;padding:4px;border-radius:9999px;background:var(--ep-track,#eef0f3)}
  input{appearance:none;-webkit-appearance:none;display:block;width:100%;height:24px;margin:0;border-radius:9999px;background:transparent;cursor:pointer;touch-action:pan-y;position:relative;z-index:2}
  .fill{position:absolute;left:4px;right:4px;top:4px;bottom:4px;border-radius:9999px;background:linear-gradient(90deg,rgb(39 177 153),rgb(59 130 246),rgb(245 158 11),rgb(239 68 68));clip-path:inset(0 calc((100% - 24px) * (1 - var(--frac,0))) 0 0 round 9999px);pointer-events:none;transition:clip-path 140ms ease-out}
  .panel.neutral .fill{filter:grayscale(1) opacity(.55)}
  input::-webkit-slider-thumb{-webkit-appearance:none;appearance:none;width:24px;height:24px;border-radius:50%;background:#fff var(--thumb) center/18px no-repeat;box-shadow:0 1px 3px rgb(0 0 0/.3)}
  input::-moz-range-thumb{width:24px;height:24px;border:0;border-radius:50%;background:#fff var(--thumb) center/18px no-repeat;box-shadow:0 1px 3px rgb(0 0 0/.3)}
  input:focus-visible{outline:2px solid rgb(var(--tone,39 177 153));outline-offset:3px}
  .dots{position:absolute;inset:0 17px;display:flex;align-items:center;justify-content:space-between;pointer-events:none;z-index:1}
  .dot{width:6px;height:6px;border-radius:50%;background:var(--ep-dot,rgb(0 0 0/.18))}
  .ends{display:flex;justify-content:space-between;margin-top:6px;font-size:11px;color:var(--ep-muted,#6b7280)}
  @media(max-width:600px){.panel{position:fixed;left:12px;right:12px;width:auto;max-width:none;bottom:var(--effort-bottom,120px)}}
  @media(prefers-reduced-motion:reduce){.needle{transition:none}.trigger{transition:none}}
  </style><button class="trigger" type="button" aria-expanded="false"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path class="arc-bg" d="${ARC}" pathLength="100"/><path class="arc" d="${ARC}" pathLength="100" stroke-dasharray="0 100"/><g class="needle"><path d="M12 14V7.5" stroke-width="1.8"/><circle cx="12" cy="14" r="1.9" fill="currentColor" stroke="none"/></g></svg></button><section class="panel" hidden aria-label="Reasoning effort"><p class="label"><small>Reasoning effort</small><span></span></p><div class="track"><div class="fill"></div><input type="range" min="0" step="1" aria-label="Reasoning effort"><div class="dots"></div></div><div class="ends"><span></span><span></span></div></section>`;
  const button=root.querySelector('.trigger'), panel=root.querySelector('.panel'), range=root.querySelector('input'), label=root.querySelector('.label span'), needle=root.querySelector('.needle'), arc=root.querySelector('.arc'), dots=root.querySelector('.dots'), ends=root.querySelectorAll('.ends span');
  let config=initial, value='', dragging=false;
  const selected=()=>config.options[effortIndex(config.options,config.value)]?.id || '';
  function render(id) {
    value=id; const index=effortIndex(config.options,id), option=config.options[index];
    const name=option?.label || 'Default';
    range.value=String(index); range.parentElement.style.setProperty('--frac',String(config.options.length>1?index/(config.options.length-1):0));
    label.textContent=name; range.setAttribute('aria-valuetext',name);
    const tip=config.disabledReason||`Reasoning effort: ${name}`; button.setAttribute('aria-label',tip); button.title=tip;
    const angle=effortAngle(config.options,id,config.defaultValue), f=effortFraction(config.options,id,config.defaultValue), tone=effortTone(config.options,id,config.defaultValue);
    needle.style.transform=`rotate(${angle}deg)`;
    arc.setAttribute('stroke-dasharray',`${f===null?0:Math.max(2,f*100)} 100`);
    for(const el of [button,panel])el.style.setProperty('--tone',tone);
    panel.classList.toggle('neutral',id==='default'||id==='auto');
    range.style.setProperty('--thumb',`url("${gaugeDataUrl(angle,f,tone)}")`);
  }
  function close(cancel=false){if(cancel)render(selected()); dragging=false;panel.hidden=true;button.setAttribute('aria-expanded','false');}
  // A parent re-render after onChange passes a fresh config with the same
  // options: keep the panel open. Only a different option list (model
  // switch) closes it and rebuilds the scale.
  const sameOptions=(a,b)=>a.length===b.length&&a.every((o,i)=>o.id===b[i].id&&o.label===b[i].label);
  let built=false;
  function update(next){if(built&&sameOptions(config.options,next.options)){config=next;if(!dragging)render(selected());return;}built=true;close(true);config=next;range.max=String(Math.max(0,next.options.length-1));range.disabled=next.options.length<2;button.disabled=next.options.length<2||!!next.disabledReason;dots.replaceChildren(...next.options.map(()=>{const dot=document.createElement('i');dot.className='dot';return dot}));ends[0].textContent=next.options[0]?.label||'';ends[1].textContent=next.options.at(-1)?.label||'';render(selected());}
  function commit(){const old=selected();if(value!==old){const next=value;config={...config,value:next};config.onChange(next)} }
  button.addEventListener('click',()=>{if(!panel.hidden){close(true);return;}if(matchMedia('(max-width:600px)').matches)panel.style.setProperty('--effort-bottom',`${Math.round(innerHeight-button.getBoundingClientRect().top+8)}px`);panel.hidden=false;button.setAttribute('aria-expanded','true');render(selected());if(!matchMedia('(pointer:coarse)').matches)range.focus({preventScroll:true});});
  range.addEventListener('pointerdown',()=>{dragging=true});
  range.addEventListener('input',()=>render(config.options[Number(range.value)]?.id||''));
  range.addEventListener('change',()=>{if(!dragging)commit()});
  range.addEventListener('pointerup',()=>{dragging=false;commit()});
  range.addEventListener('pointercancel',()=>{dragging=false;render(selected())});
  root.addEventListener('keydown',e=>{if(e.key==='Escape'){e.stopPropagation();close(true);button.focus()}});
  const outside=e=>{if(!panel.hidden&&!e.composedPath().includes(host))close(true)};
  document.addEventListener('pointerdown',outside,true);update(initial);
  return {update,destroy(){document.removeEventListener('pointerdown',outside,true);root.replaceChildren()}};
}
