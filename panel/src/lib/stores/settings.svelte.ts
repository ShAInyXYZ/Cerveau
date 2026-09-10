import { j, jpost } from '../api';
type Thinking={mode:string;effort:string;modes:string[];efforts:string[]};
type Sampling={active:string;presets:string[]};
let thinking=$state<Thinking>({mode:'plan',effort:'low',modes:[],efforts:[]});
let sampling=$state<Sampling>({active:'default',presets:[]});
let error=$state(''),busy=$state(false);
export const settingsStore={
 get thinking(){return thinking;},get sampling(){return sampling;},get error(){return error;},get busy(){return busy;},
 async load(){const [t,s]=await Promise.all([j<Thinking>('/api/thinking'),j<Sampling>('/api/sampling')]);if(t)thinking=t;if(s)sampling=s;},
 async setThinking(patch:Partial<Thinking>){
  if(busy)return;busy=true;error='';const next={...thinking,...patch};
  try{const t=await jpost<{mode:string;effort:string}>('/api/thinking',{mode:next.mode,effort:next.effort});thinking={...thinking,...t};}
  catch(e){error=e instanceof Error?e.message:String(e);}finally{busy=false;}
 },
 async setSampling(name:string){
  if(busy)return;busy=true;error='';
  try{const s=await jpost<{active:string}>('/api/sampling',{name});sampling={...sampling,...s};}
  catch(e){error=e instanceof Error?e.message:String(e);}finally{busy=false;}
 }
};
