// Fixture-only browser acceptance. All /api traffic is intercepted; no Core,
// production sessions, installed packs, desktop picker or external site is used.
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = resolve(fileURLToPath(new URL('..', import.meta.url)));
const { createServer } = await import(join(root, 'panel/node_modules/vite/dist/node/index.js'));
const { chromium } = await import(process.env.CERVEAU_PLAYWRIGHT_MODULE || 'playwright');
mkdirSync(join(root, 'build'), { recursive: true });
const evidence = mkdtempSync(join(root, 'build/rfx-ui-acceptance-'));
const server = await createServer({ root: join(root, 'panel'), server: { host: '127.0.0.1', port: 0, proxy: {} }, logLevel: 'error' });
await server.listen();
const base = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, executablePath: process.env.CERVEAU_CHROMIUM || undefined });
const receipts = [];
try {
 for (const [name, width, height] of [['desktop',1440,1000],['mobile',390,844]]) {
  const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block' });
  const page = await context.newPage(); const errors = [], posts = [];
  page.on('pageerror', e => errors.push(e.message));
  const messages = Array.from({length:18}, (_, i) => ({ id:`m${i}`,type:i%2?'msg.assistant':'msg.user',ts:'2026-09-05T10:00:00Z',payload:{text:`Fixture ${i}: ${'A bounded inspection preserves the source evidence. '.repeat(7)}`}}));
  const run = { id:'manual',kind:'reflex',reflex:'git-status',status:'failed',step:-1,calls:0,control_version:0 };
  const snapshot = { messages,run,running:false,logs:{},events:[],errors:[{id:'incident',run_id:'manual',what:'Git status failed',why:'Fixture command failure'}],plan_state:{plan_event_id:'plan',title:'Finished fixture plan',next:7,blocked:-1,done:true,steps:Array.from({length:7},(_,i)=>({id:`s${i}`,title:`Step ${i+1}`,status:'passed',rev:0,attempts:1}))} };
  const schema = {type:'object',properties:{url:{type:'string'},checks:{type:'array',items:{type:'object',properties:{selector:{type:'string'},kind:{type:'string',enum:['exists']},expected:{type:'boolean'}},required:['selector','kind','expected']}}},required:['url','checks']};
  const rfx = { packs:[{name:'devcheck',version:'0.1.0',icon:'bug',ui:{widgets:[{type:'field',name:'url',label:'Local app URL'},{type:'button',label:'Inspect app',run:'devcheck-inspect'},{type:'log',lines:8}]}}], reflexes:[{name:'devcheck-inspect',pack:'devcheck',description:'Inspect bounded browser facts',risk:'safe',enabled:true,params:{type:'object',properties:{url:{type:'string'}},required:['url']}},{name:'devcheck-check',pack:'devcheck',description:'Run declared checks',risk:'safe',enabled:true,params:schema}] };
  rfx.reflexes.push({name:'fixture-publish',pack:'devcheck',description:'Fixture only; exercises host confirmation',risk:'dangerous',enabled:true,params:{type:'object',properties:{label:{type:'string'}},required:['label']}});
  await page.route('**/api/**', async route => {
    const request = route.request(), url = new URL(request.url());
    let body = {};
    if (request.method() !== 'GET') {
      const posted = request.postDataJSON(); posts.push({path:url.pathname,body:posted});
      if(url.pathname.endsWith('/commands')) body={run:{...run,id:'command',kind:'',status:'completed'}};
      else if(url.pathname==='/api/rfx/run') body={ok:true,output:JSON.stringify({ok:true,verdict:'observed',summary:{title:'Fixture app'}})};
      else throw Error('Unexpected fixture mutation '+url.pathname);
    } else if(url.pathname==='/api/health') body={components:[],workspace:'/fixture',model:{name:'Fixture Core',modalities:{text:true,vision:true}},system:{version:'0.6.0-alpha'}};
    else if(url.pathname==='/api/sessions') body={sessions:[{id:'fixture',name:'RFX acceptance',workspace:'/fixture'}]};
    else if(url.pathname==='/api/running') body={sessions:[]};
    else if(url.pathname.endsWith('/state')) body=snapshot;
    else if(url.pathname.endsWith('/stream')) return route.fulfill({status:200,contentType:'text/event-stream',body:': fixture\n\n'});
    else if(url.pathname==='/api/rfx') body=rfx;
    else if(url.pathname==='/api/skills') body={skills:[]};
    else if(url.pathname==='/api/idle') body={state:'active',enabled:false};
    else if(url.pathname==='/api/thinking') body={mode:'plan',effort:'low',efforts:['low','medium','xhigh']};
    else if(url.pathname==='/api/sampling') body={active:'strict',presets:['default','strict','neutral','creative']};
    return route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(body)});
  });
  // Simulated display stream proves stop-after-one-frame wiring, not an actual
  // browser/OS permission dialog. Real desktop permission remains operator QA.
  await page.addInitScript(() => {
    window.__captureStops = 0;
    Object.defineProperty(navigator,'mediaDevices',{value:{getDisplayMedia:async()=>{
      const canvas=document.createElement('canvas');canvas.width=1600;canvas.height=1000;
      const ctx=canvas.getContext('2d');ctx.fillStyle='#ddd';ctx.fillRect(0,0,1600,1000);ctx.fillStyle='#111';ctx.font='40px sans-serif';ctx.fillText('Capture fixture — crop before compression',80,100);
      const stream=canvas.captureStream(10);const track=stream.getVideoTracks()[0];const stop=track.stop.bind(track);track.stop=()=>{window.__captureStops++;stop();};return stream;
    }},configurable:true});
  });
  await page.goto(base+'/?rfx=devcheck');
  await page.getByRole('button',{name:'Attach image or capture screen',exact:true}).waitFor();
  await page.getByText('Reflex: git-status.',{exact:false}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Retry unfinished step',exact:true}).count(),0);
  await page.waitForFunction(()=>{const e=document.querySelector('.stream');return e&&e.scrollHeight-e.scrollTop-e.clientHeight<10;});
  const before=await page.locator('.stream').evaluate(e=>e.scrollHeight);
  messages.at(-1).payload.text+='\n\n'+('Additional streamed facts.\n'.repeat(25));
  await page.waitForFunction(old=>document.querySelector('.stream').scrollHeight>old,before);
  await page.waitForFunction(()=>{const e=document.querySelector('.stream');return e.scrollHeight-e.scrollTop-e.clientHeight<10;});
  await page.locator('.stream').evaluate(e=>{e.scrollTop=0;});
  await page.getByRole('button',{name:'Jump to latest',exact:false}).waitFor();
  messages.at(-1).payload.text+='\n'+('More observations.\n'.repeat(25));
  await page.waitForTimeout(2200);
  assert.equal(await page.locator('.stream').evaluate(e=>e.scrollTop),0,'tail stole scroll position');
  await page.getByRole('button',{name:'Jump to latest',exact:false}).click();
  assert.equal(posts.length,0,'page mount or status polling started a Reflex');
  const input=page.getByRole('textbox',{name:'message input',exact:true});
  await input.fill('first line\nsecond line\nthird line');
  assert.ok(await input.evaluate(e=>e.clientHeight)>55,'composer did not grow');
  await input.dispatchEvent('keydown',{key:'Enter',isComposing:true});
  assert.equal(posts.length,0,'IME Enter submitted a message');
  await page.getByRole('button',{name:'Attach image or capture screen',exact:true}).click();
  await page.getByRole('button',{name:'Capture window or screen',exact:true}).click();
  await page.getByRole('img',{name:'Captured source before cropping',exact:true}).waitFor();
  assert.equal(await page.evaluate(()=>window.__captureStops),1,'display sharing was not stopped');
  await page.getByLabel('Width',{exact:true}).fill('400');
  await page.getByLabel('Height',{exact:true}).fill('300');
  if (!process.env.CERVEAU_QA_NO_SCREENSHOTS) await page.screenshot({path:join(evidence,`${name}-capture.png`),fullPage:true});
  await page.getByRole('button',{name:'Attach preview',exact:true}).click();
  await page.locator('.attachment').waitFor();
  const image = await page.locator('.attachment img').getAttribute('src');
  assert.ok(image.startsWith('data:image/jpeg;base64,'));
  assert.ok(Buffer.from(image.split(',')[1],'base64').length<=256*1024);
  if (!process.env.CERVEAU_QA_NO_SCREENSHOTS) await page.screenshot({path:join(evidence,`${name}-chat.png`),fullPage:true});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`${name} horizontal overflow`);
  const padding = await page.locator('.stream').evaluate(e=>parseFloat(getComputedStyle(e).paddingBottom));
  assert.ok(padding<=24,'obsolete composer offset remains');
  await page.getByRole('button',{name:'send message',exact:true}).click();
  await page.waitForFunction(()=>!document.querySelector('.attachment'));
  const submitted = posts.find(p=>p.path.endsWith('/commands'))?.body;
  assert.equal(submitted.images.length,1);assert.equal(submitted.images[0].data_url,image);
  await page.locator('.all-actions summary').click();
  const checkCard = page.locator('.card').filter({hasText:'devcheck-check'});
  await checkCard.getByRole('textbox').nth(0).fill('http://localhost:5171');
  await checkCard.getByRole('textbox').nth(1).fill('{broken');
  const priorPosts = posts.length;
  await checkCard.getByRole('button',{name:'Run',exact:true}).click();
  await checkCard.getByRole('alert').waitFor();
  assert.equal(posts.length,priorPosts,'invalid JSON was dispatched');
  const checks = [{selector:'main',kind:'exists',expected:true}];
  await checkCard.getByRole('textbox').nth(1).fill(JSON.stringify(checks));
  await checkCard.getByRole('button',{name:'Run',exact:true}).click();
  await page.waitForTimeout(100);
  assert.deepEqual(posts.at(-1).body.args.checks,checks,'structured fields were sent as strings');
  const dangerCard = page.locator('.card').filter({hasText:'fixture-publish'});
  await dangerCard.getByRole('textbox').fill('draft');
  const beforeDanger = posts.length;
  await dangerCard.getByRole('button',{name:'Run',exact:true}).click();
  assert.equal(posts.length,beforeDanger,'first dangerous click executed');
  await dangerCard.getByRole('textbox').fill('changed');
  await dangerCard.getByRole('button',{name:'Run',exact:true}).click();
  assert.equal(posts.length,beforeDanger,'changed arguments reused approval');
  await dangerCard.getByRole('button',{name:'Confirm run',exact:true}).click();
  await page.waitForTimeout(100);
  assert.equal(posts.at(-1).body.confirmed,true); assert.equal(posts.at(-1).body.args.label,'changed');
  assert.deepEqual(errors,[]);
  receipts.push({viewport:name,width,height,paddingBottom:padding,passed:['tail growth','scroll anchoring','manual Reflex identity','no mount-time actions','textarea growth','IME guard','simulated single-frame stop','source crop','JPEG byte cap','image command','typed All actions','invalid JSON refusal','argument-bound human confirmation','no horizontal overflow'],limitations:['API fixtures, not live Core','simulated capture permission']});
  await context.close();
 }
 writeFileSync(join(evidence,'results.json'),JSON.stringify(receipts,null,2)+'\n');
 console.log(JSON.stringify({ok:true,evidence,receipts},null,2));
} finally { await browser.close(); await server.close(); }
