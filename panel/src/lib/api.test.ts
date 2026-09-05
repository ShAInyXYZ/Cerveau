import {test,vi,expect} from 'vitest';
test('HTTP failures preserve status and message',async()=>{
 vi.stubGlobal('localStorage',{getItem:()=>null});vi.stubGlobal('location',{origin:'http://audit.invalid'});vi.stubGlobal('window',globalThis);
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({error:'run already active'}),{status:409})));
 const {api}=await import('./api');
 await expect(api.rewind('s','evt')).rejects.toMatchObject({status:409,message:'run already active'});
});
