// Loaded only after explicit consent to the browser model download. No tools, business APIs or credentials.
import { CreateMLCEngine } from '@mlc-ai/web-llm'
self.onmessage=async(event:MessageEvent<string>)=>{
  try {
    const engine=await CreateMLCEngine('Qwen2.5-0.5B-Instruct-q4f32_1-MLC',{logLevel:'SILENT',initProgressCallback:r=>self.postMessage({progress:Math.round(r.progress*100)})},{context_window_size:4096})
    const out=await engine.chat.completions.create({messages:[{role:'user',content:event.data}],temperature:0,max_tokens:600,response_format:{type:'json_object'}})
    self.postMessage({text:out.choices[0]?.message.content || ''})
  } catch { self.postMessage({error:'本地模型未能完成推理。请检查 WebGPU、模型下载网络和设备可用内存，或使用键值文本解析。'}) }
}
