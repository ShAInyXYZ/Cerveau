const labels: Record<string,string> = {
  done:'verified', needs_reverify:'awaiting recheck', blocked:'blocked',
  running:'running', verifying:'checking', pending:'not started',
  unverified:'unverified', skipped:'skipped',
};
function normalized(status:string):string {
  if(status==='passed') return 'done';
  if(status==='failed') return 'blocked';
  return status in labels ? status : 'unverified';
}
export function planStatusLabel(status:string):string { return labels[normalized(status)]; }
export function planCounts(steps:ReadonlyArray<{status:string}>) {
  const counts: Record<string,number> = {};
  for(const step of steps) { const key=normalized(step.status); counts[key]=(counts[key]??0)+1; }
  return Object.entries(labels).filter(([status])=>counts[status]>0).map(([status,label])=>({status,label,count:counts[status]}));
}
