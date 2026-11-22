const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

class Element {
  constructor(tag) {
    this.tag=tag;this.children=[];this.ownText='';this.innerHTML='';this.dataset={};this.listeners={};this.attributes={};
    const classes=new Set();
    this.classList={add:n=>classes.add(n),remove:n=>classes.delete(n),contains:n=>classes.has(n),toggle:(n,on)=>on?classes.add(n):classes.delete(n)};
  }
  set textContent(text){this.ownText=String(text ?? '');this.children=[];}
  get textContent(){return this.ownText+this.children.map(child=>child.textContent).join('');}
  addEventListener(event,handler){this.listeners[event]=handler;}
  setAttribute(name,value){this.attributes[name]=value;}
  appendChild(child){this.children.push(child);}
  replaceChildren(...children){this.children=children;}
  scrollIntoView(){}
  focus(){}
}
async function viewer(sessionPage=()=>({items:[]}), url='http://localhost/', sessionSummary=()=>({ok:false})){
  const elements=new Map(), requests=[], sessionRequests=[], documentListeners={};
  const element=id=>{if(!elements.has(id))elements.set(id,new Element());return elements.get(id);};
  const context=vm.createContext({URL,Element,AbortController,location:{href:url},document:{querySelector:element,createElement:tag=>new Element(tag),
    addEventListener:(event,handler)=>{documentListeners[event]=handler;}},
    EventSource:class{constructor(){this.listeners={};}close(){this.closed=true;}addEventListener(event,handler){this.listeners[event]=handler;}},
    fetch:(url,{signal}={})=>{
      if(url==='/api/sessions'||url.startsWith('/api/sessions?')){
        sessionRequests.push(url);
        return Promise.resolve(sessionPage(url)).then(body=>({ok:true,json:async()=>body}));
      }
      if(url.endsWith('/summary')) return Promise.resolve(sessionSummary(url));
      return new Promise((resolve,reject)=>requests.push({url,signal,reject,
        resolve:body=>resolve({ok:true,json:async()=>body,text:async()=>JSON.stringify(body)}),
        resolveText:text=>resolve({ok:true,text:async()=>text})}));
    }});
  for (const script of ['detail.js','app.js']) vm.runInContext(readFileSync(join(__dirname,'assets',script),'utf8'),context);
  const app=vm.runInContext('({state,openSession,showDetail,refreshPromptPanel,renderInline,classify,passesFilter,ingestBatch,showDiagnostics})',context);
  await new Promise(setImmediate);return {...app,element,requests,sessionRequests,documentListeners};
}
const event=(seq,type,payload={},source)=>({seq,type,v:1,time:1000,payload,...(source?{source}:{})});
const batch=(sequence,events)=>({tail:{sequence,hash:'hash'+sequence,count:sequence},batch:{id:'batch'+sequence,records:[{type:'app',name:'san.events',data:{version:1,events,validation:{}}}]}});
const snapshot=text=>({context:{System:text,Tools:[],Messages:[]},history:[]});
const textBlock=text=>({type:'text',text});
const message=(id,role,...content)=>({id,role,content});
const descendants=node=>[node,...node.children.flatMap(descendants)];
function group(root,title){
  const node=root.children.find(el=>el.className==='detail-group' && el.children[0].textContent===title);
  assert.ok(node,title);
  return node;
}
function panel(root,title){
  const node=descendants(root).find(el=>el.tag==='details' && el.children[0].textContent.startsWith(title));
  assert.ok(node,title);
  return node;
}
function expand(root,title){
  const node=panel(root,title);
  node.open=true;node.listeners.toggle();return node;
}
const originalJSON=root=>expand(root,'Original JSON').children.at(-1).textContent;

test('model response metrics distinguish missing usage, explicit zero and reported cache hit',async()=>{
  const v=await viewer();
  v.state.currentSession='fixture';
  const usages=[undefined,{input:0,output:0,cache_read:0},{input:12,output:3,cache_read:1200,cache_write:40,reasoning:2}];
  v.ingestBatch(batch(1,usages.flatMap((usage,i)=>[
    event(2*i+1,'inference.start',{purpose:'turn'}),
    event(2*i+2,'inference.finish',{outcome:'success',usage},{seq:2*i+1}),
  ])));
  const root=v.element('#detail-body');
  await v.showDetail(1);
  assert.match(root.textContent,/Input tokensUnavailableOutput tokensUnavailableCache-read tokensUnavailableCache-write tokensUnavailable/);
  await v.showDetail(3);
  assert.match(root.textContent,/Input tokens0Output tokens0Cache-read tokens0Cache-write tokensNot reported/);
  assert.doesNotMatch(root.textContent,/reported hit/);
  await v.showDetail(5);
  assert.ok(root.textContent.includes(`Cache-read tokens${(1200).toLocaleString()} · reported hitCache-write tokens40`));
  assert.match(expand(root,'Raw usage').textContent,/"reasoning": 2/);
});

test('Update response compares SDK prefix separately from cache metrics',async()=>{
  const v=await viewer();
  v.state.currentSession='fixture';
  v.ingestBatch(batch(1,[
    event(1,'inference.start',{purpose:'memory-update'}),
    event(2,'inference.finish',{outcome:'success',usage:{input:1,output:2}},{seq:1}),
  ]));
  await v.showDetail(1);
  assert.deepEqual(v.requests.map(r=>r.url),['/api/sessions/fixture/request/1?view=update-prefix']);
  v.requests[0].resolve({status:'match',source:7,count:4});
  await new Promise(setImmediate);
  const root=v.element('#detail-body');
  assert.match(root.textContent,/Cache-read tokensNot reported/);
  assert.match(root.textContent,/Update SDK prefix/);
  assert.match(root.textContent,/Matches completed main request and its assistant outputMatching messages4Compared main request#7/);
});

test('session pages load only on navigation or refresh and prevent overlapping requests',async()=>{
  let complete;
  const v=await viewer(url=>url.includes('?') ? new Promise(resolve=>{complete=resolve;}) : {items:[{id:'recent',title:'Latest user message'}],next:'cursor+1'});
  const next=v.element('#sessions-next'), prev=v.element('#sessions-prev'), status=v.element('#sessions-page');
  assert.equal(status.textContent,'Page 1');
  assert.equal(prev.disabled,true);
  next.listeners.click();next.listeners.click();v.element('#sessions-refresh').listeners.click();
  assert.deepEqual(v.sessionRequests,['/api/sessions','/api/sessions?cursor=cursor%2B1']);
  assert.equal(next.disabled,true);
  complete({items:[{id:'older'}]});await new Promise(setImmediate);
  assert.equal(v.element('#session-list').textContent,'Untitled session');
  assert.equal(status.textContent,'Page 2');
  assert.equal(next.disabled,true);
  prev.listeners.click();await new Promise(setImmediate);
  assert.equal(v.element('#session-list').textContent,'Latest user message');
  assert.equal(status.textContent,'Page 1');
});

test('session row keeps metadata inside the clickable button and supports a direct link',async()=>{
  const v=await viewer(()=>({items:[{id:'fresh',title:'New session',updatedAt:'2026-11-21T00:25:32Z'}]}), 'http://localhost/?session=fresh');
  assert.equal(v.state.currentSession,'fresh');
  const row=v.element('#session-list').children[0];
  assert.equal(row.classList.contains('active'),true);
  const [button]=row.children;
  assert.equal(row.children.length,1);
  assert.equal(button.tag,'button');
  assert.equal(button.children[0].textContent,'New session');
  assert.equal(button.children[1].className,'id');
  v.openSession('another');
  button.listeners.click();
  assert.equal(v.state.currentSession,'fresh');
});

test('subagent identity appears in the sidebar and off-page header',async()=>{
  const v=await viewer(()=>({items:[{id:'listed',subagent:true}]}), 'http://localhost/?session=child', url=>({
    ok:url.endsWith('/child/summary'), json:async()=>({title:'Review tests',subagent:true,parent:'parent'}),
  }));
  assert.equal(v.element('#session-list').textContent,'Subagent');
  assert.equal(v.element('#session-meta').textContent,'Subagent · Review tests');
  assert.equal(v.element('#session-parent').hidden,false);
  assert.equal(v.element('#session-parent').href,'?session=parent');
  v.openSession('large');
  assert.equal(v.element('#session-parent').hidden,true);
  v.ingestBatch(batch(1,[event(1,'session.create',{agent_id:'worker-address',parent:{session:'other'}})]));
  assert.equal(v.element('#session-meta').textContent,'Subagent');
  assert.equal(v.element('#session-parent').href,'?session=other');
});

test('expanding details preserves the selected step, payload, and timeline',async()=>{
  const v=await viewer();v.openSession('native');
  v.ingestBatch(batch(1,[event(1,'annotation.add',{text:'recorded detail'}),event(2,'message.append')]));
  await v.showDetail(0);
  const detail=v.element('#detail-body'), timeline=v.element('#timeline'), button=v.element('#detail-expand');
  const raw=expand(detail,'Raw batch');
  const rows=timeline.children, selection=v.state.selectedRecordID;
  assert.equal(JSON.parse(originalJSON(raw)).batch.records[0].data.events[0].payload.text,'recorded detail');
  button.listeners.click();
  assert.equal(v.element('main').classList.contains('detail-wide'),true);
  assert.equal(button.attributes['aria-expanded'],'true');
  assert.equal(button.attributes['aria-label'],'Restore details width');
  assert.equal(timeline.children,rows);
  assert.equal(v.state.selectedRecordID,selection);
  assert.equal(raw.open,true);
  assert.equal(v.requests.length,1,'widening the pane does not reload context');
  v.documentListeners.keydown({key:'Escape',preventDefault(){}});
  assert.equal(v.element('main').classList.contains('detail-wide'),false);
  assert.equal(button.attributes['aria-label'],'Expand details');
});

test('session settings, projected context, and stored JSON have separate scopes',async()=>{
  const v=await viewer();v.openSession('native');
  v.ingestBatch(batch(1,[event(1,'session.create',{config:{model:'fixture-model-v1'}})]));
  await v.showDetail(0);
  const detail=v.element('#detail-body');
  assert.equal(panel(detail,'Event').open,false);
  assert.equal(panel(detail,'Raw batch').open,false);
  const recorded=expand(detail,'Event'), projected=panel(detail,'Context');
  assert.equal(detail.children[2],projected);
  assert.equal(projected.open,true);
  assert.match(recorded.textContent,/Session settings.*fixture-model-v1/s);
  assert.equal(v.requests.length,1);
  v.requests[0].resolve(snapshot('projected prompt'));
  await new Promise(setImmediate);
  assert.match(projected.textContent,/not a model request/);
  assert.doesNotMatch(projected.textContent,/fixture-model-v1/);
  assert.match(expand(projected,'System prompt').textContent,/projected prompt/);
  assert.doesNotMatch(recorded.textContent,/projected prompt/);
});

test('instruction text comes before the change trigger',async()=>{
  const v=await viewer();v.openSession('native');
  v.ingestBatch(batch(1,[event(1,'instructions.update',{set:{project:{slot:1,content:'Project rules'}},cause:'boot'})]));
  await v.showDetail(0);
  const detail=v.element('#detail-body');
  assert.equal(detail.children[2].children[0].textContent,'Context');
  assert.match(expand(detail,'Event').textContent,/Project rules.*Prompt order1.*Triggered byStartup/s);
  assert.equal(v.requests.length,1);
});

test('late detail responses cannot overwrite another record or session',async()=>{
  for(const change of ['record','session'])for(const fail of [false,true]){
    const v=await viewer();v.openSession('first');v.ingestBatch(batch(1,[event(1,'inference.start'),event(2,'message.append')]));
    const pending=v.showDetail(0);
    if(change==='session')v.openSession('second');else v.showDetail(1);
    assert.equal(v.requests[0].signal.aborted,true);
    const detail=v.element('#detail-body'),before=detail.textContent;
    if(fail)v.requests[0].reject(new Error('stale failure'));else v.requests[0].resolve(snapshot('obsolete'));
    await pending;
    assert.equal(detail.textContent,before,`${change} failure=${fail}`);
  }
});
test('late prompt responses cannot overwrite the selected record',async()=>{
  const v=await viewer();v.openSession('first');
  v.ingestBatch(batch(1,[event(1,'message.append'),event(2,'message.append')]));
  const old=v.showDetail(0);v.element('#toggle-prompt').listeners.click();
  const current=v.showDetail(1);
  assert.equal(v.requests.length,2,'detail and prompt reuse the selected snapshot');
  v.requests[1].resolve(snapshot('current'));await current;await new Promise(setImmediate);
  const body=v.element('#prompt-body'),before=body.textContent;
  assert.match(before,/current/);
  v.requests[0].resolve(snapshot('obsolete'));await old;
  assert.equal(body.textContent,before);
});
test('retired streams and diagnostics cannot change a new session',async()=>{
  const v=await viewer();v.openSession('first');const old=v.state.eventSource;
  const pending=v.showDiagnostics('first',old);v.openSession('second');
  old.onmessage({data:JSON.stringify(batch(1,[event(1,'annotation.add')]))});old.onerror();
  old.listeners.diagnostic({data:JSON.stringify({error:'old error'})});
  const before=v.element('#detail-body').children;
  v.requests[0].resolve({error:'old',unvalidatedText:'<script>bad</script>'});await pending;
  assert.equal(v.state.records.length,0);assert.equal(v.element('#detail-body').children,before);
});
test('event labels share a mapping and fall back to the raw type for unknown data',async()=>{
  const v=await viewer();v.openSession('native');
  v.ingestBatch(batch(1,[event(1,'session.create'),event(2,'inference.finish',{outcome:'success'}),
    event(3,'future.event'),{...event(4,'session.update'),v:2}]));
  const rows=v.element('#timeline').children;
  assert.deepEqual(rows.map(row=>row.title),['session.create','inference.finish','future.event','session.update']);
  assert.deepEqual(rows.map(row=>row.textContent),['#1Session · Start','#2Model · ResponseSuccess',
    '#3future.eventUnsupported (raw)','#4session.updateUnsupported (raw)']);
  await v.showDetail(0);
  assert.match(v.element('#detail-body').textContent,/#1 · session\.createSession · Start/);
  await v.showDetail(3);
  assert.match(v.element('#detail-body').textContent,/#4 · session\.updatesession\.update/);
});

test('native batches retain all 13 event families and sequence/source provenance',async()=>{
  const v=await viewer();v.openSession('native');
  const types=['session.create','session.update','message.append','context.compact','inference.start','inference.finish','permission.decide','tool.finish','instructions.update','toolset.update','skills.update','hook.finish','annotation.add'];
  const envelope=batch(1,types.map((type,i)=>event(i+1,type,{text:'native'},i===12?{session:'child',seq:7}:undefined)));
  v.ingestBatch(envelope);v.ingestBatch(envelope);
  const rows=v.element('#timeline').children;
  assert.equal(v.state.records.length,13);assert.equal(rows.length,13);
  assert.deepEqual(rows.slice(8,11).map(row=>row.textContent),['#9Instructions','#10Tools','#11Skills']);
  assert.match(rows[12].textContent,/#13Annotationnative← child#7/);
  const headings=['Session identity','Event','Recorded messages','Compaction','Model call','Inference outcome','Permission decision','Tool execution','Instruction changes','Tool changes','Skill changes','Hook execution','Annotation'];
  for(const [index,type] of types.entries()){
    assert.notEqual(v.classify(type),'unknown',type);
    const pending=v.showDetail(index);
    assert.ok(expand(v.element('#detail-body'),'Event').textContent.includes(headings[index]),type);
    if(type==='inference.start') v.requests.at(-1).resolve(snapshot('native'));
    await pending;
  }
  v.state.filters.state=false;assert.equal(v.passesFilter(event(1,'session.update')),false);
});
test('a single skill change names the skill and its recorded state',async()=>{
  const v=await viewer();v.openSession('native');
  v.ingestBatch(batch(1,[event(1,'skills.update',{set:{review:'active'},cause:'user'})]));
  assert.equal(v.element('#timeline').children[0].textContent,'#1Skillsreview · active');
  await v.showDetail(0);
  assert.match(expand(v.element('#detail-body'),'Event').textContent,/Does not rewrite the system prompt/);
});

test('acceptance and consumption remain distinct facts while native history stays deduplicated',async()=>{
  const v=await viewer();v.openSession('native');
  const queued=message('queued','user',textBlock('next prompt'));
  const accepted=batch(1,[event(4,'message.append',{messages:[queued],accepted:true})]);
  const consumed=batch(2,[event(7,'message.append',{messages:[queued]},{seq:4})]);
  v.ingestBatch(accepted);const first=v.element('#timeline').children[0];
  v.ingestBatch(consumed);v.ingestBatch(accepted);v.ingestBatch(consumed);
  assert.equal(v.state.records.length,2);
  assert.notEqual(v.state.records[0]._key,v.state.records[1]._key);
  const timeline=v.element('#timeline');
  assert.equal(timeline.children[0],first,'live batches do not replace earlier rows');
  assert.equal(first.textContent,'#4Message · Queuednext prompt');
  assert.equal(timeline.children[1].textContent,'#7Message · Usernext prompt← #4');
  const pending=v.showDetail(1), detail=v.element('#detail-body');
  assert.match(expand(detail,'Event').textContent,/next prompt/);
  await pending;
  assert.match(v.requests[0].url,/\/state\/7$/);
  assert.doesNotMatch(detail.textContent,/history/i);
  const history=v.element('#show-history').listeners.click();
  assert.match(v.requests[1].url,/\/state\/0$/);
  v.requests[1].resolve({seq:7,context:{Messages:[queued]},history:[queued]});await history;
  assert.match(detail.textContent,/Session history.*Through step #7.*Messages · 1/s);
  assert.match(expand(detail,'User · queued').textContent,/next prompt/);
  await v.showDetail(1);
  descendants(expand(detail,'Event')).find(el=>el.tag==='button' && el.textContent==='#4').listeners.click();
  assert.match(expand(detail,'Event').textContent,/Accepted input · queued for consumption/);
  assert.equal(first.attributes['aria-current'],'true');
});
test('timeline separates hook status, message preview and source from raw payloads',async()=>{
  const v=await viewer();v.openSession('native');
  const command='node -e "'+'long command '.repeat(40)+'"';
  const reminder='<system-reminder source="memory-project">\nproject context\n</system-reminder>';
  const notice='<system-reminder>\nnotice\n</system-reminder>';
  const promptHook='<system-reminder source="hook-user-prompt-submit">\nhook context\n</system-reminder>';
  const skills='<system-reminder source="skills-directory">\nvisible skills\n</system-reminder>';
  const userEvent=(seq,text)=>event(seq,'message.append',{messages:[message('m'+seq,'user',textBlock(text))]});
  v.ingestBatch(batch(1,[event(1,'hook.finish',{hook:command,trigger:'SessionStart',outcome:'blocked',duration_ms:87}),
    userEvent(2,['你好',promptHook,skills,reminder,notice].join('\n\n')),userEvent(3,reminder),userEvent(4,'literal '+reminder),
    event(5,'inference.finish',{outcome:'success',stop_reason:'end_turn',duration_ms:12},{seq:2}),
    userEvent(6,'x'.repeat(400))]));
  const timeline=v.element('#timeline'), [hook,user,system,literal,finish,long]=timeline.children;
  const byClass=(row,name)=>descendants(row).find(node=>node.className?.split(' ').includes(name));
  assert.equal(byClass(hook,'step-label').textContent,'Hook · Session start');
  assert.equal(byClass(hook,'step-status').textContent,'Blocked');
  assert.match(byClass(hook,'step-status').className,/failed/);
  assert.equal(byClass(hook,'step-preview').textContent,'87 ms');
  assert.doesNotMatch(hook.textContent,/node -e/);
  assert.equal(byClass(user,'step-preview').textContent,'你好');
  assert.equal(byClass(system,'step-preview').textContent,'System reminder');
  assert.match(byClass(literal,'step-preview').textContent,/literal <system-reminder/);
  assert.equal(byClass(finish,'step-source').textContent,'← #2');
  assert.equal(byClass(finish,'step-preview').textContent,'end turn · 12 ms');
  assert.equal(byClass(long,'step-preview').textContent,'x'.repeat(180)+'…');
  assert.equal(v.element('#timeline-count').textContent,'6 steps');
  v.element('#filter-hook').listeners.change({target:{checked:false}});
  assert.equal(v.element('#timeline-count').textContent,'5 of 6 steps');
  const detail=v.element('#detail-body');
  v.showDetail(0);assert.ok(expand(detail,'Event').textContent.includes(command));
  await v.showDetail(1);
  const details=expand(detail,'Event');
  assert.doesNotMatch(details.textContent,/project context|notice/);
  const reminders=expand(details,'System reminders · 4');
  assert.match(reminders.textContent,/User prompt hook.*hook context.*Skills directory.*visible skills.*Project instructions.*project context.*Source not recorded.*notice/s);
  const raw=originalJSON(expand(detail,'Raw batch'));
  const events=JSON.parse(raw).batch.records[0].data.events;
  assert.ok(events.find(rec=>rec.seq===2).payload.messages[0].content[0].text.includes(reminder));
});
test('standalone inference renders the admitted system prompt, messages and tool schemas using SDK keys',async()=>{
  const v=await viewer();v.openSession('native');
  v.ingestBatch(batch(1,[event(1,'inference.start',{purpose:'judge',input:{standalone:'digest'},request:{model:'model'}})]));
  const pending=v.showDetail(0);
  v.element('#toggle-prompt').listeners.click();
  const request={System:'admitted system',Messages:[message('request','user',textBlock('admitted message'))],
    Tools:[{name:'Read',description:'read a file',definition:{type:'object'}}]};
  v.requests[0].resolve({...snapshot('ambient context'),request});await pending;
  const detail=v.element('#detail-body'),prompt=v.element('#prompt-body');
  assert.match(prompt.textContent,/admitted system/);
  assert.doesNotMatch(prompt.textContent,/ambient context|empty system prompt/);
  assert.match(expand(prompt,'Read').textContent,/read a file.*"type": "object"/s);
  assert.match(expand(detail,'User · request').textContent,/admitted message/);
  const input=group(detail,'Model input');
  assert.match(expand(input,'System prompt').textContent,/admitted system/);
  assert.match(expand(input,'User · request').textContent,/admitted message/);
  assert.doesNotMatch(expand(detail,'Event').textContent,/admitted system|admitted message/);
  assert.doesNotMatch(input.textContent,/View JSON/);
});
test('Aside — Auto Memory shows its paired response on live arrival and links both ways',async()=>{
  const v=await viewer();v.openSession('native');
  v.ingestBatch(batch(1,[event(1,'inference.start',{purpose:'recall',input:{standalone:'blob'}}),
    event(2,'inference.start',{purpose:'turn'})]));
  const pending=v.showDetail(0),detail=v.element('#detail-body');
  assert.match(group(detail,'Model output').textContent,/No response recorded yet/);
  v.requests[0].resolve({...snapshot('context'),request:{System:'selector',Messages:[
    message('index','user',textBlock('Memory files:\n- user/sign-off.md: Commit rule\n- project/freeze.md: Release freeze\n')),
    message('context','user',textBlock('Already in main context:\n- user/sign-off.md\n')),
    message('query','user',textBlock('Can I merge today?'))]}});await pending;
  const input=group(detail,'Model input');
  assert.match(input.textContent,/Candidates for new recall · 1.*project\/freeze\.md: Release freeze.*Already in main context · 1.*user\/sign-off\.md.*User question.*Can I merge today\?/s);
  assert.doesNotMatch(input.textContent,/Commit rule/);
  const text='{"selected_memories":["project/freeze.md"]}';
  v.ingestBatch(batch(2,[event(3,'message.append',{messages:[message('answer','assistant',textBlock('main answer'))]},{seq:2}),
    event(4,'inference.finish',{outcome:'success'},{seq:2}),
    event(5,'inference.finish',{outcome:'success',output:{text}},{seq:1})]));
  const rows=v.element('#timeline').children;
  assert.match(rows[0].textContent,/Model Request · Aside — Auto Memory/);
  assert.match(rows[4].textContent,/Model Response · Aside — Auto Memory/);
  let output=group(detail,'Model output');
  assert.match(output.textContent,/Files selected by the model · 1.*project\/freeze\.md/s);
  assert.equal(originalJSON(output),text);
  assert.match(output.textContent,/No accepted result recorded/);
  assert.doesNotMatch(output.textContent,/main answer/);
  const accepted='<img src=x onerror=alert(1)> attached';
  v.ingestBatch(batch(3,[event(6,'annotation.add',{text:accepted},{seq:1}),
    event(7,'annotation.add',{text:'foreign source'},{session:'other',seq:1}),
    event(8,'message.append',{messages:[message('result','user',textBlock('linked reminder'))]},{seq:1})]));
  assert.match(output.textContent,/Raw model output.*selected_memories.*Accepted result.*attached.*linked reminder/s);
  assert.ok(output.textContent.includes(accepted));
  assert.ok(!descendants(output).some(el=>el.tag==='img'));
  assert.doesNotMatch(output.textContent,/foreign source|No accepted result recorded/);
  const jump=(root,label)=>descendants(root).find(el=>el.tag==='button' && el.textContent===label).listeners.click();
  jump(output,'#5');
  output=group(detail,'Model output');
  assert.equal(originalJSON(output),text);
  jump(output,'#1');
  assert.match(detail.textContent,/Model Request · Aside — Auto Memory/);
  v.requests.at(-1).resolve({...snapshot('context'),request:{System:'selector',Messages:[message('legacy','user',
    textBlock('Already in main context:\n- user/legacy.md\n'),
    textBlock('Memory files:\n- project/old.md: Old note\n\nUser query:\n'),textBlock('Recorded question'))]}});
  await new Promise(setImmediate);
  assert.match(group(detail,'Model input').textContent,/project\/old\.md: Old note.*user\/legacy\.md.*Recorded question/s);
  await v.showDetail(3);
  assert.match(group(detail,'Model output').textContent,/main answer/);
});

test('every model purpose labels its request and response consistently',async()=>{
  const v=await viewer();v.openSession('native');
  const purposes=[['turn',''],['btw','By the Way',true],['recall','Auto Memory',true,'Recall'],
    ['memory-update','Auto Memory',true,'Update'],['auto-skill','Auto Skill',true,'Review'],
    ['recap','Recap',true],['suggest','Suggestion',true],
    ['judge','Judge Side'],['flow','Flow Draft',true],['autopilot','AutoPilot',true],
    ['group','Group Side'],['compaction','Compaction'],['hook','Hook'],['new-purpose','new-purpose']];
  v.ingestBatch(batch(1,purposes.flatMap(([purpose],i)=>{
    const seq=2*i+1;
    return [event(seq,'inference.start',{purpose}),event(seq+1,'inference.finish',{outcome:'success',output:{text:purpose+' answer'}},{seq})];
  })));
  const rows=v.element('#timeline').children,detail=v.element('#detail-body');
  for(const [i,[purpose,label,side,operation]] of purposes.entries()){
    const suffix=side?' · Aside — '+label:label?' · '+label:'';
    assert.equal(rows[2*i].textContent,`#${2*i+1}${side?'Model Request':'Model · Request'}${suffix}`);
    assert.ok(rows[2*i+1].textContent.includes(`${side?'Model Response':'Model · Response'}${suffix}${side?'Model completed':'Success'}`));
    await v.showDetail(2*i+1);
    assert.ok(group(detail,'Model output').textContent.includes(purpose+' answer'),purpose);
    if(operation) {
      assert.ok(expand(detail,'Event').textContent.includes('Operation'+operation));
      const pending=v.showDetail(2*i);
      assert.ok(expand(detail,'Event').textContent.includes('Operation'+operation));
      v.requests.at(-1).resolve({...snapshot('context'),request:{System:'memory'}});
      await pending;
    }
  }
});

test('older By the Way records show the linked reply instead of borrowing another annotation',async()=>{
  const v=await viewer();v.openSession('native');
  v.ingestBatch(batch(1,[event(1,'inference.start',{purpose:'btw'}),
    event(2,'annotation.add',{text:'recorded side answer'},{seq:1}),
    event(3,'annotation.add',{text:'unrelated note'}),
    event(4,'inference.finish',{outcome:'success'},{seq:1})]));
  await v.showDetail(3);
  const output=group(v.element('#detail-body'),'Model output');
  assert.match(output.textContent,/Raw model output.*No model output was recorded.*Accepted result.*recorded side answer/s);
  assert.doesNotMatch(output.textContent,/unrelated note/);
  descendants(output).find(el=>el.tag==='button' && el.textContent==='#2').listeners.click();
  assert.match(v.element('#detail-body').textContent,/#2 · annotation.add/);
});

test('tool details join the invocation, rendered file diff and model-facing result',async()=>{
  const v=await viewer();v.openSession('native');
  const input='{"path":"file.txt","new_string":"after"}';
  const call={type:'tool_call',tool_call:{id:'call-id',name:'Edit',input}};
  const result={type:'tool_result',tool_result:{tool_call_id:'call-id',tool_name:'Edit',content:[textBlock('File updated')]}};
  v.ingestBatch(batch(1,[event(2,'message.append',{messages:[message('call','assistant',call)]}),
    event(3,'permission.decide',{call_id:'call-id',decision:'allow',by:'user',asked:{options:['Allow','Deny'],detail:{prompt:'Apply edit?'}}}),
    event(4,'message.append',{messages:[message('result','user',result)]}),
    event(5,'tool.finish',{call_id:'call-id',outcome:'success',duration_ms:0,details:{Path:'file.txt',EditCount:1,AddedLines:1,RemovedLines:1,
      UnifiedDiff:'@@ -1 +1 @@\n-before\n+after\n'}},{seq:2})]));
  const done=v.showDetail(3), detail=v.element('#detail-body');
  assert.match(v.element('#timeline').children[3].textContent,/Tool · ExecutionSuccessEdit/);
  const details=expand(detail,'Event');
  assert.equal(originalJSON(details),input);
  assert.match(details.textContent,/Duration0 ms.*File changes.*Model-facing result.*File updated/s);
  const diff=descendants(details).filter(el=>el.className?.startsWith('diff-'));
  assert.deepEqual(diff.map(el=>[el.className,el.textContent.trim()]),[['diff-header','@@ -1 +1 @@'],['diff-remove','-before'],['diff-add','+after']]);
  await done;
  assert.equal(v.requests.length,1,'opening Event shares the selected context request');
  v.showDetail(1);
  assert.match(expand(detail,'Event').textContent,/Permission decision.*Authorityuser.*Allow, Deny.*Apply edit\?.*Requested invocation/s);
});
test('Bash and Agent show recorded details without inventing output',async()=>{
  const v=await viewer();v.openSession('native');
  v.ingestBatch(batch(1,[event(1,'tool.finish',{call_id:'bash',outcome:'failure',details:{lineCount:8,error:'exit 1',tail:'last line',tailTruncated:true}}),
    event(2,'tool.finish',{call_id:'agent',outcome:'success',child_session:'child',details:{Identity:{Name:'review'},Response:'review result'}}),
    event(3,'tool.finish',{call_id:'background',outcome:'success',details:{lineCount:0,taskId:'work'}})]));
  const detail=v.element('#detail-body');
  v.showDetail(0);assert.match(expand(detail,'Event').textContent,/Shell output.*exit 1.*Recorded tail \(truncated\).*last line/s);
  v.showDetail(1);assert.match(expand(detail,'Event').textContent,/Child sessionchild.*Agent result.*review result/s);
  v.showDetail(2);assert.match(expand(detail,'Event').textContent,/Background taskwork/);assert.doesNotMatch(detail.textContent,/Recorded output/);
});
test('unknown and malformed events stay visible as inert raw text',async()=>{
  const v=await viewer();v.openSession('future');
  v.ingestBatch(batch(1,[event(1,'future.event',{text:'<img src=x onerror=alert(1)>'}),null]));
  assert.equal(v.element('#timeline').children.length,2);
  assert.match(v.element('#timeline').children[0].textContent,/Unsupported \(raw\)/);
  await v.showDetail(0);
  assert.equal(v.requests.length,0);
  const detail=v.element('#detail-body');
  assert.ok(originalJSON(expand(detail,'Raw batch')).includes('<img src=x onerror=alert(1)>'));
  assert.ok(descendants(detail).every(node=>node.innerHTML===''));
});
test('diagnostic raw bytes are bounded server data rendered as text, not HTML',async()=>{
  const v=await viewer();v.openSession('broken');const pending=v.showDiagnostics('broken',v.state.eventSource);
  v.requests[0].resolve({error:'hash mismatch',unvalidatedText:'<script>bad()</script>'});await pending;
  assert.equal(v.element('#detail-body').children.at(-1).textContent,'<script>bad()</script>');
  assert.equal(v.element('#detail-body').children.at(-1).innerHTML,'');
});
test('raw event and SDK request preserve their distinct JSON and integer precision',async()=>{
  const v=await viewer();v.openSession('exact');
  const wire=JSON.stringify(batch(1,[event(1,'inference.start',{sampling_params:{seed:'NUMBER'}})])).replace('"NUMBER"','9007199254740993');
  v.state.eventSource.onmessage({data:wire});
  const pending=v.showDetail(0);
  const raw='{"System":"native","Messages":[{"role":"user","content":[{"type":"text","text":"original"},{"type":"text","text":"\\n\\n<system-reminder>\\nworkspace\\n</system-reminder>"}]}],"SamplingParams":{"seed":9007199254740993}}';
  v.requests[0].resolveText('{"context":{"System":"native"},"request":'+raw+'}');await pending;
  const detail=v.element('#detail-body');
  assert.equal(originalJSON(expand(detail,'Raw batch')),wire);
  assert.equal(v.requests.length,1,'raw request is lazy');
  const request=expand(group(detail,'Model input'),'Raw SDK request');
  assert.match(v.requests[1].url,/\/exact\/request\/1$/);
  assert.equal(v.requests[1].signal,v.requests[0].signal);
  v.requests[1].resolveText(raw);await new Promise(setImmediate);
  const params=panel(request,'"SamplingParams":');
  assert.equal(params.open,false);
  assert.equal(params.children.length,1,'collapsed branches do not build their children');
  expand(request,'"SamplingParams":');
  assert.match(params.textContent,/"seed": 9007199254740993/);
  params.open=false;params.listeners.toggle();
  expand(request,'"SamplingParams":');
  assert.equal(params.children.length,2,'reopening does not duplicate the branch');
  const messages=expand(request,'"Messages":');
  const first=expand(messages,'[0]:');
  assert.match(first.textContent,/"role": "user"/);
  assert.equal(panel(first,'"content":').open,false);
  assert.equal(originalJSON(request),raw);
});
test('Markdown links cannot execute transcript content',async()=>{
  const v=await viewer();
  for(const url of ['javascript:alert%281%29','JaVaScRiPt:alert%281%29','data:text/html,hello','vbscript:msgbox','//evil.test','javascript&#58;alert','\u0000javascript:alert'])assert.ok(!v.renderInline(`[link](${url})`).includes('<a '),url);
  for(const url of ['https://example.com','http://localhost/','mailto:a@example.com'])assert.ok(v.renderInline(`[link](${url})`).includes('<a '),url);
  const html=v.renderInline('[<img src=x>](https://example.com/"onmouseover="bad)');
  assert.ok(!html.includes('<img'));assert.ok(html.includes('noopener noreferrer'));assert.ok(!html.includes('href="https://example.com/"'));
});
