// Opt-in embedded-panel QA. Start TestLABRIGUIPreview first; no browser API mocks.
const { chromium } = await import(process.env.CERVEAU_PLAYWRIGHT_MODULE || 'playwright');
const base='http://127.0.0.1:17706';
const browser=await chromium.launch({headless:true,executablePath:process.env.CERVEAU_CHROMIUM || undefined});
const errors=[];
try {
 for(const [name,width,height] of [['desktop',1440,1000],['mobile',390,844]]) {
  await fetch(base+'/__labrig/start',{method:'POST'});
  const context=await browser.newContext({viewport:{width,height},serviceWorkers:'block'});
  const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
  // No routes mocked: this is the production panel talking to the real Go API.
  await page.goto(base);
  await page.getByRole('img',{name:'V0.6',exact:true}).waitFor();
  await page.getByRole('button',{name:'Pause',exact:true}).click();
  await page.getByRole('button',{name:'Resume',exact:true}).waitFor();
  const sessions=await(await fetch(base+'/api/sessions')).json();
  const sid=sessions.sessions[0].id;
  let snapshot=await(await fetch(base+'/api/sessions/'+sid+'/state')).json();
  const runID=snapshot.run.id;
  if(snapshot.run.status!=='paused' && snapshot.run.status!=='pause_requested')throw Error('pause was not recorded');
  await page.reload();
  await page.getByRole('button',{name:'Resume',exact:true}).waitFor();
  snapshot=await(await fetch(base+'/api/sessions/'+sid+'/state')).json();
  if(snapshot.run.id!==runID)throw Error('reload replaced run');
  await page.screenshot({path:`/tmp/cerveau-labrig-${name}.png`,fullPage:true});
  if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error(name+' horizontal overflow');
  // A disconnected observer must retain its confirmed owner and say unknown.
  await context.setOffline(true);
  await page.getByRole('status').filter({hasText:'Connection lost; run state unknown'}).waitFor({timeout:6000});
  await context.setOffline(false);
  await page.getByRole('status').filter({hasText:'Connection lost; run state unknown'}).waitFor({state:'hidden',timeout:8000});
  await page.getByRole('button',{name:'Resume',exact:true}).click();
  await page.getByRole('button',{name:'Pause',exact:true}).waitFor();
  await page.getByRole('button',{name:'Stop',exact:true}).click();
  await page.getByRole('button',{name:'send message',exact:true}).waitFor();
  snapshot=await(await fetch(base+'/api/sessions/'+sid+'/state')).json();
  if(snapshot.run.id!==runID || snapshot.run.status!=='cancelled' || snapshot.running)throw Error('stop not confirmed by server');
  console.log(`${name}: real API matrix, pause/reload/same-owner, offline/reconnect, resume/stop and overflow PASS`);
  await context.close();
 }
 if(errors.length)throw Error(errors.join('\n'));
} finally {await browser.close();}
