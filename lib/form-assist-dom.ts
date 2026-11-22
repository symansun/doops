import { containsSecret, forbiddenLabel, type FieldSpec } from './form-assist'
export const scopeSelector='form,[data-assist-scope],[role="dialog"]'
type Control=HTMLInputElement|HTMLSelectElement|HTMLTextAreaElement
export type Bound=FieldSpec & {el:Control; original:string}
function labelOf(el:Control, trigger:Element|null) {
  const direct=el.getAttribute('aria-label')||trigger?.getAttribute('aria-label')
  if(direct)return direct.trim()
  const label=el.labels?.[0]||el.closest('label')||trigger?.closest('label')
  if(!label)return el.name || ''
  const walker=document.createTreeWalker(label,NodeFilter.SHOW_TEXT);const text:string[]=[]
  for(let n=walker.nextNode();n;n=walker.nextNode())if(!n.parentElement?.closest('input,select,textarea,button,option,[data-assist-ui]'))text.push(n.textContent||'')
  return text.join(' ').trim().replace(/\s+/g,' ').slice(0,80)
}
export function fieldsOf(scope:HTMLElement):Bound[]{
  const result:Bound[]=[]
  for(const el of scope.querySelectorAll<Control>('input,select,textarea')){
    if(el.closest(scopeSelector)!==scope||el.closest('[data-assist-ui],[data-assist-exclude]')||el.matches(':disabled,[readonly]'))continue
    if(el instanceof HTMLInputElement&&!['text','number','url','email','tel','date','datetime-local','search'].includes(el.type))continue
    if(el instanceof HTMLTextAreaElement&&!el.hasAttribute('data-assist-text'))continue
    const trigger=el instanceof HTMLSelectElement?el.previousElementSibling:null
    if(!(el.hidden&&trigger?.getAttribute('role')==='combobox'?trigger:el).getClientRects().length)continue
    const label=labelOf(el,trigger)
    if(!label||forbiddenLabel.test([label,el.name,el.id,el.autocomplete,el.getAttribute('placeholder')].join(' ')))continue
    const options=el instanceof HTMLSelectElement?Array.from(el.options).filter(o=>!o.disabled&&o.value!==''&&!containsSecret(o.label)).map(o=>o.label).slice(0,100):undefined
    result.push({el,id:'f'+result.length,label,original:el.value,max:el instanceof HTMLSelectElement?256:el.maxLength>0?Math.min(1024,el.maxLength):512,options,numeric:el.type==='number',min:el instanceof HTMLInputElement&&el.min?Number(el.min):undefined,ceiling:el instanceof HTMLInputElement&&el.max?Number(el.max):undefined})
  }
  return result
}
export function applyOne(el:Control,value:string){
 const select=el instanceof HTMLSelectElement,option=select?Array.from(el.options).find(o=>!o.disabled&&o.label===value):undefined
 if(select&&!option)return false
 const proto=select?HTMLSelectElement.prototype:el instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype
 const setter=Object.getOwnPropertyDescriptor(proto,'value')?.set;if(!setter)return false
 setter.call(el,option?.value??value);el.dispatchEvent(new Event(select?'change':'input',{bubbles:true}));return true
}
