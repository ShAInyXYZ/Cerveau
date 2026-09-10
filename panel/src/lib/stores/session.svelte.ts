// Server projections own execution state; UI requests never own the worker.
import { api, ApiError, streamEvents, type RunControl } from '../api';
import { toStep, errorKey } from '../steps';
import { storage, storageKeys } from '../storage';
import { play } from '../sound.js';
import { ErrorChime } from '../errorchime';
import { healthStore } from './health.svelte.ts';
import type { ChatMessage, EpisodicEvent, LiveStep, Mode, PlanReport, Question, SessionError, SessionMeta, RunState, PlanState } from '../types';

let sessions=$state<SessionMeta[]>([]), runningIds=$state<string[]>([]);
let activeId=$state<string|null>(null), mode=$state<Mode>(initialMode());
let messages=$state<ChatMessage[]>([]), ticks=$state<EpisodicEvent[]>([]);
let logs=$state<Record<string,EpisodicEvent[]>>({}), lastEvents=$state<Record<string,EpisodicEvent>>({});
let errors=$state<SessionError[]>([]), question=$state<Question|null>(null), report=$state<PlanReport|null>(null);
let run=$state<RunState|null>(null), plan=$state<PlanState|null>(null), requestError=$state('');
let skills=$state<unknown[]>([]), turnSampling=$state(''), submitting=$state<string[]>([]);
let generation=0, refreshSeq=0, timer:ReturnType<typeof setInterval>|null=null, closeStream:(()=>void)|null=null;
let connectionLost=$state(false);
let streamTimer:ReturnType<typeof setTimeout>|null=null;
let dismissed=new Set<string>();
const errorChime=new ErrorChime();
function incidentKey(e:SessionError,i:number) {
 return JSON.stringify([activeId,e.run_id??run?.id??'legacy',e.id??errorKey(e,i)]);
}
const pendingCommands=new Map<string,{key:string;id:string}>();
const pendingControls=new Map<string,{key:string;body:RunControl}>();
function recallPending<T>(kind:string,sid:string,map:Map<string,T>):T|undefined {
 if(map.has(sid))return map.get(sid);
 try { const raw=sessionStorage.getItem('crv.pending.'+kind+'.'+sid);if(raw){const value=JSON.parse(raw) as T;map.set(sid,value);return value;} }catch{}
}
function savePending<T>(kind:string,sid:string,map:Map<string,T>,value?:T) {
 if(value)map.set(sid,value);else map.delete(sid);
 try { const key='crv.pending.'+kind+'.'+sid;if(value)sessionStorage.setItem(key,JSON.stringify(value));else sessionStorage.removeItem(key); }catch{}
}
const activeStatuses=new Set(['running','paused','pause_requested','waiting_user','cancelling']);
function initialMode():Mode { try { const m=localStorage.getItem('crv.mode'); if(m==='discussion'||m==='brainstorming') return m; }catch{} return 'autopilot'; }
function current(sid:string,g:number) { return activeId===sid && generation===g; }
function busy() { return !!activeId && (submitting.includes(activeId)||runningIds.includes(activeId)||!!run&&activeStatuses.has(run.status)); }
function failure(e:unknown) { requestError=e instanceof Error?e.message:String(e); }

function subscribe(sid:string,g:number,cursor:string) {
 if(closeStream||!current(sid,g))return;
 const update=()=>{if(!current(sid,g)||streamTimer)return;streamTimer=setTimeout(()=>{streamTimer=null;void refresh();},100);};
 closeStream=streamEvents(sid,update,cursor,update);
}
async function refresh() {
 const sid=activeId,g=generation,seq=++refreshSeq;if(!sid)return;
 const state=await api.sessionState(sid);
 if(!current(sid,g)||seq!==refreshSeq)return;
 if(!state){connectionLost=true;return;}
 connectionLost=false;
 if(Array.isArray(state.messages))messages=state.messages;
 logs=state.logs??{};
 const previous=run;run=state.run??null;plan=state.plan_state??null;
 if(previous?.id===run?.id && previous && activeStatuses.has(previous.status) && run?.status==='completed')play('done');
 runningIds=runningIds.filter(id=>id!==sid);
 if(state.running)runningIds=[...runningIds,sid];
 ticks=(state.events??[]).slice(-200);
 if(ticks.length)lastEvents={...lastEvents,[sid]:ticks[ticks.length-1]};
 question=state.question?.question?state.question:null;
 errors=(state.errors??[]).filter((e,i)=>!dismissed.has(incidentKey(e,i))).slice(-3);
 if(errorChime.shouldPlay(errors.map(incidentKey)))play('error');
 report=state.report??null;
 subscribe(sid,g,state.cursor??'');
}
async function runControl(action:'pause'|'resume'|'kill'|'steer',text=''):Promise<boolean> {
 const sid=activeId,g=generation,target=run;if(!sid||!target)return false;
 const key=JSON.stringify([target.id,action,text]);
 const saved=recallPending('control',sid,pendingControls);
 const body=saved?.key===key?saved.body:{run_id:target.id,control_id:crypto.randomUUID(),control_version:target.control_version??0};
 savePending('control',sid,pendingControls,{key,body});
 try {
  const accepted=action==='steer'?await api.steer(sid,text,body):await api[action](sid,body);
  savePending('control',sid,pendingControls);
  if(current(sid,g)){run=accepted.run;requestError='';await refresh();}
  return true;
 }catch(e){
  if(e instanceof ApiError && e.status>=400 && e.status<500)savePending('control',sid,pendingControls);
  if(current(sid,g)){failure(e);await refresh();}
  return false;
 }
}
async function control(fn:(sid:string)=>Promise<unknown>):Promise<boolean> {
 const sid=activeId,g=generation;if(!sid)return false;
 try { await fn(sid);if(current(sid,g)){requestError='';await refresh();}return true; }
 catch(e){if(current(sid,g))failure(e);return false;}
}
async function command(body:Record<string,unknown>):Promise<boolean> {
 const sid=activeId,g=generation;if(!sid||busy()||connectionLost)return false;
 const key=JSON.stringify(body),saved=recallPending('command',sid,pendingCommands);
 const command_id=saved?.key===key?saved.id:crypto.randomUUID();
 savePending('command',sid,pendingCommands,{key,id:command_id});
 submitting=[...submitting,sid];requestError='';
 try {
  const accepted=await api.command(sid,{...body,command_id});
  savePending('command',sid,pendingCommands);
  if(current(sid,g)){run=accepted.run;turnSampling='';await refresh();}
  return true;
 }catch(e){
  if(e instanceof ApiError && e.status>=400 && e.status<500)savePending('command',sid,pendingCommands);
  if(current(sid,g))failure(e);return false;
 }finally{submitting=submitting.filter(id=>id!==sid);}
}

export const sessionStore={
 get sessions(){return sessions;},get activeId(){return activeId;},
 get activeIsInstant(){return sessions.find(s=>s.id===activeId)?.instant===true;},
 get workspace(){return sessions.find(s=>s.id===activeId)?.workspace??healthStore.workspace;},
 get messages(){return messages;},get ticks(){return ticks;},get lastEvents(){return lastEvents;},
 get running(){return busy();},get submitting(){return !!activeId&&submitting.includes(activeId);},
 get connectionLost(){return connectionLost;},get run(){return run;},get plan(){return plan;},get requestError(){return requestError;},
 get runStarted(){return run&&activeStatuses.has(run.status)?Date.parse(run.started):null;},
 get windowReport(){return run?.result?.window??null;},get question(){return question;},get errors(){return errors;},get report(){return report;},
 get logs(){return logs;},get skills(){return skills;},get runningIds(){return runningIds;},
 get turnSampling(){return turnSampling;},set turnSampling(v:string){turnSampling=v;},
 get mode(){return mode;},set mode(v:Mode){mode=v;try{localStorage.setItem('crv.mode',v);}catch{}},
 get liveSteps(){return ticks.filter(e=>!run||e.payload?.run_id===run.id).map(toStep).filter((v):v is LiveStep=>!!v);},
 async loadSessions(){sessions=await api.sessions();runningIds=await api.runningSessions();if(!activeId&&sessions.length)this.select(sessions[0].id);},
 async loadSkills(){skills=await api.skills();},
 select(id:string){
  errorChime.reset();
  generation++;connectionLost=false;if(streamTimer)clearTimeout(streamTimer);streamTimer=null;activeId=id;messages=[];ticks=[];errors=[];logs={};question=null;report=null;run=null;plan=null;requestError='';turnSampling='';
  dismissed=new Set(storage.get<string[]>(storageKeys.dismissedErrors(id),[]));
  closeStream?.();closeStream=null;void refresh();
 },
 async send(text:string,opts:{step?:boolean;images?:{data_url:string}[]}={}){return command({kind:'chat',text,mode,sampling:turnSampling,...(opts.images?.length?{images:opts.images.map(({data_url})=>({data_url}))}:{})});},
 async editAndResend(eventId:string,text:string){
  const sid=activeId,g=generation;
  const images=messages.find(m=>m.id===eventId)?.payload?.images?.map(({data_url})=>({data_url}));
  if(!sid||busy()||(!text.trim()&&!images?.length))return;
  if(!await control(id=>api.rewind(id,eventId)))return;
  if(current(sid,g))await this.send(text,{images});
 },
 async panelTurn(text:string,m?:Mode){if(m)mode=m;return this.send(text);},
 async steer(text:string){return runControl('steer',text);},
 async pause(){return runControl('pause');},async resume(){return runControl('resume');},async kill(){return runControl('kill');},
 async answer(ans:string){const q=question;return control(id=>api.answer(id,ans,q?.id,q?.run_id));},
 async runAutopilot(){return command({kind:'continue',plan_event_id:plan?.plan_event_id});},
 async runStep(step=-1,revision=false){return command({kind:'step',step,revision,plan_event_id:plan?.plan_event_id});},
 async dismissAllErrors(){if(!activeId)return;dismissed=new Set([...dismissed,...errors.map(incidentKey)]);storage.set(storageKeys.dismissedErrors(activeId),[...dismissed]);errors=[];},
 async retry(text:string){if(run?.kind==='reflex')return false;if(plan&&!plan.done)return command({kind:'step',step:plan.blocked>=0?plan.blocked:-1,plan_event_id:plan.plan_event_id,continue_plan:true});const images=[...messages].reverse().find(m=>m.type==='msg.user')?.payload?.images;return this.send(text,{images});},
 async create(name:string,workspace?:string){try{const m=await api.createSession(name,workspace);await this.loadSessions();if(m?.id)this.select(m.id);}catch(e){failure(e);}},
 async createInstant(){try{const m=await api.createInstant();await this.loadSessions();if(m?.id)this.select(m.id);}catch(e){failure(e);}},
 async rename(id:string,name:string){if(await api.renameSession(id,name))await this.loadSessions();},
 async remove(id:string,m:string,confirm:string){const ok=await api.deleteSession(id,m,confirm);if(ok){if(activeId===id){generation++;activeId=null;messages=[];closeStream?.();}await this.loadSessions();}return ok;},
 async onWorkspaceChanged(ws:string){await this.create(ws.replace(/[/\\]+$/,'').split(/[/\\]/).pop()||'session',ws);},
 start(){if(timer)return;void this.loadSessions();void this.loadSkills();timer=setInterval(()=>{void refresh();void api.runningSessions().then(ids=>runningIds=ids);},2000);},
 stop(){if(timer)clearInterval(timer);timer=null;generation++;closeStream?.();closeStream=null;},
};
