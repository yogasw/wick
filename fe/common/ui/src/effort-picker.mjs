import { effortAngle, effortIndex } from './effort-ui.mjs';

// The caller owns persistence and capabilities. This component never invents options.
export function mountEffortPicker(host, initial) {
  const root = host.shadowRoot || host.attachShadow({mode:'open'});
  root.innerHTML = `<style>
  :host{display:inline-flex;position:relative;color:inherit;font:inherit}
  button{color:inherit;background:transparent;border:0;cursor:pointer;font:inherit}
  .trigger{display:grid;place-items:center;width:36px;height:36px;border-radius:50%;color:var(--effort-accent,#8aa9ff)}
  .trigger:hover,.trigger:focus-visible{background:#8882;outline:2px solid currentColor;outline-offset:2px}
  svg{width:25px;height:25px}.needle{transform-origin:12px 13px;transition:transform 140ms ease-out}
  .panel{position:absolute;bottom:calc(100% + 14px);right:0;width:min(340px,calc(100vw - 32px));box-sizing:border-box;padding:20px;border-radius:24px;background:var(--effort-panel,#27272a);color:var(--effort-text,#fff);box-shadow:0 8px 40px #0006;border:1px solid #8884;z-index:50;text-align:center}
  .panel[hidden]{display:none}.label{font-size:24px;font-weight:650;margin:0 0 18px;line-height:1.2}.label span{color:var(--effort-accent,#8aa9ff)}
  .track{height:54px;padding:0 10px;border-radius:40px;background:var(--effort-track,#414145);display:grid;align-items:center;position:relative;box-sizing:border-box}
  .dots{position:absolute;inset:0 27px;display:flex;align-items:center;justify-content:space-between;pointer-events:none}.dot{width:8px;height:8px;background:#ffffff60;border-radius:50%}
  input{appearance:none;-webkit-appearance:none;width:100%;height:42px;margin:0;background:linear-gradient(to right,var(--effort-fill,#315ee5) var(--progress),#555 var(--progress));border-radius:30px;cursor:pointer;touch-action:pan-y;z-index:1;position:relative}
  input::-webkit-slider-thumb{appearance:none;width:36px;height:36px;background:white;border-radius:50%;box-shadow:0 1px 4px #0003}input::-moz-range-thumb{width:36px;height:36px;border:0;background:white;border-radius:50%}
  input:focus-visible{outline:2px solid var(--effort-accent,#8aa9ff);outline-offset:4px}.dots{z-index:2}.ends{display:flex;justify-content:space-between;font-size:11px;opacity:.7;margin-top:10px}
  @media(max-width:600px){.panel{position:fixed;left:16px;right:16px;width:auto;bottom:var(--effort-bottom,120px)}}
  @media(prefers-reduced-motion:reduce){.needle{transition:none}}
  </style><button class="trigger" type="button" aria-expanded="false"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M4 19a10 10 0 1 1 16 0"/><g class="needle"><path d="M12 13V6"/><circle cx="12" cy="13" r="2" fill="currentColor" stroke="none"/></g></svg></button><section class="panel" hidden aria-label="Reasoning effort"><p class="label"><span></span></p><div class="track"><input type="range" min="0" step="1" aria-label="Reasoning effort"><div class="dots"></div></div><div class="ends"><span></span><span></span></div></section>`;
  const button=root.querySelector('.trigger'), panel=root.querySelector('.panel'), range=root.querySelector('input'), label=root.querySelector('.label span'), needle=root.querySelector('.needle'), dots=root.querySelector('.dots'), ends=root.querySelectorAll('.ends span');
  let config=initial, value='', dragging=false;
  const selected=()=>config.options[effortIndex(config.options,config.value)]?.id || '';
  function render(id) {
    value=id; const index=effortIndex(config.options,id), option=config.options[index];
    const name=option?.label || 'Default';
    range.value=String(index); range.style.setProperty('--progress',`${config.options.length>1?index/(config.options.length-1)*100:0}%`);
    label.textContent=name; range.setAttribute('aria-valuetext',name);
    button.setAttribute('aria-label',`Reasoning effort: ${name}`); button.title=`Reasoning effort: ${name}`;
    needle.style.transform=`rotate(${effortAngle(config.options,id,config.defaultValue)}deg)`;
  }
  function close(cancel=false){if(cancel)render(selected()); dragging=false;panel.hidden=true;button.setAttribute('aria-expanded','false');}
  function update(next){close(true);config=next;range.max=String(Math.max(0,next.options.length-1));range.disabled=next.options.length<2;button.disabled=next.options.length<2;dots.replaceChildren(...next.options.map(()=>{const dot=document.createElement('i');dot.className='dot';return dot}));ends[0].textContent=next.options[0]?.label||'';ends[1].textContent=next.options.at(-1)?.label||'';render(selected());}
  function commit(){const old=selected();if(value!==old){const next=value;config={...config,value:next};config.onChange(next)} }
  button.addEventListener('click',()=>{if(!panel.hidden){close(true);return;}panel.hidden=false;button.setAttribute('aria-expanded','true');render(selected());if(!matchMedia('(pointer:coarse)').matches)range.focus({preventScroll:true});});
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
