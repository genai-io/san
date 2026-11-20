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
}
async function viewer(){
  const elements=new Map(), requests=[];
  const element=id=>{if(!elements.has(id))elements.set(id,new Element());return elements.get(id);};
  const context=vm.createContext({URL,Element,document:{querySelector:element,createElement:tag=>new Element(tag)},
    EventSource:class{constructor(){this.listeners={};}close(){this.closed=true;}addEventListener(event,handler){this.listeners[event]=handler;}},
    setInterval(){},fetch:url=>{
      if(url==='/api/sessions')return Promise.resolve({ok:true,json:async()=>[]});
      return new Promise((resolve,reject)=>requests.push({url,reject,
        resolve:body=>resolve({ok:true,json:async()=>body,text:async()=>JSON.stringify(body)}),
        resolveText:text=>resolve({ok:true,text:async()=>text})}));
    }});
  for (const script of ['detail.js','app.js']) vm.runInContext(readFileSync(join(__dirname,'assets',script),'utf8'),context);
  const app=vm.runInContext('({state,openSession,showDetail,refreshPromptPanel,renderInline,classify,passesFilter,ingestBatch,showDiagnostics})',context);
  await new Promise(setImmediate);return {...app,element,requests};
}
const event=(seq,type,payload={},source)=>({seq,type,v:1,time:1000,payload,...(source?{source}:{})});
const batch=(sequence,events)=>({tail:{sequence,hash:'hash'+sequence,count:sequence},batch:{id:'batch'+sequence,records:[{type:'app',name:'san.events',data:{version:1,events,validation:{}}}]}});
const snapshot=text=>({context:{System:text,Tools:[],Messages:[]},history:[]});
const textBlock=text=>({type:'text',text});
const message=(id,role,...content)=>({id,role,content});
const descendants=node=>[node,...node.children.flatMap(descendants)];
function expand(root,title){
  const node=descendants(root).find(el=>el.tag==='details' && el.children[0].textContent.startsWith(title));
  assert.ok(node,title);
  node.open=true;node.listeners.toggle();return node;
}

test('late detail responses cannot overwrite another record or session',async()=>{
  for(const change of ['record','session'])for(const fail of [false,true]){
    const v=await viewer();v.openSession('first');v.ingestBatch(batch(1,[event(1,'inference.start'),event(2,'message.append')]));
    const pending=v.showDetail(0);
    if(change==='session')v.openSession('second');else v.showDetail(1);
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
test('native batches retain all 13 event families and sequence/source provenance',async()=>{
  const v=await viewer();v.openSession('native');
  const types=['session.create','session.update','message.append','context.compact','inference.start','inference.finish','permission.decide','tool.finish','instructions.update','toolset.update','skills.update','hook.finish','annotation.add'];
  const envelope=batch(1,types.map((type,i)=>event(i+1,type,{text:'native'},i===12?{session:'child',seq:7}:undefined)));
  v.ingestBatch(envelope);v.ingestBatch(envelope);
  assert.equal(v.state.records.length,13);assert.equal(v.element('#timeline').children.length,13);
  assert.match(v.element('#timeline').children[12].textContent,/#13Annotationnative← child#7/);
  const headings=['Session identity','Session changes','Recorded messages','Compaction','Model call','Inference outcome','Permission decision','Tool execution','Catalogue changes','Catalogue changes','Catalogue changes','Hook execution','Annotation'];
  for(const [index,type] of types.entries()){
    assert.notEqual(v.classify(type),'unknown',type);
    const pending=v.showDetail(index);
    assert.ok(v.element('#detail-body').textContent.includes(headings[index]),type);
    if(type==='inference.start') v.requests.at(-1).resolve(snapshot('native'));
    await pending;
  }
  v.state.filters.state=false;assert.equal(v.passesFilter(event(1,'session.update')),false);
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
  assert.equal(first.textContent,'#4Input queuednext prompt');
  assert.equal(timeline.children[1].textContent,'#7User messagenext prompt← #4');
  const pending=v.showDetail(1), detail=v.element('#detail-body');
  assert.match(detail.textContent,/next prompt/);
  await pending;
  assert.equal(v.requests.length,0,'recorded details do not fetch the latest session');
  assert.doesNotMatch(detail.textContent,/history/i);
  const history=v.element('#show-history').listeners.click();
  assert.match(v.requests[0].url,/\/state\/0$/);
  v.requests[0].resolve({seq:7,context:{Messages:[queued]},history:[queued]});await history;
  assert.match(detail.textContent,/Session history.*Through step #7.*Messages · 1/s);
  assert.match(expand(detail,'User · queued').textContent,/next prompt/);
  await v.showDetail(1);
  descendants(detail).find(el=>el.tag==='button' && el.textContent==='#4').listeners.click();
  assert.match(detail.textContent,/Accepted input · queued for consumption/);
  assert.equal(first.attributes['aria-current'],'true');
});
test('timeline separates hook status, message preview and source from raw payloads',async()=>{
  const v=await viewer();v.openSession('native');
  const command='node -e "'+'long command '.repeat(40)+'"';
  const reminder='<system-reminder source="memory-project">\nproject context\n</system-reminder>';
  const notice='<system-reminder>\nnotice\n</system-reminder>';
  const userEvent=(seq,text)=>event(seq,'message.append',{messages:[message('m'+seq,'user',textBlock(text))]});
  v.ingestBatch(batch(1,[event(1,'hook.finish',{hook:command,trigger:'SessionStart',outcome:'blocked',duration_ms:87}),
    userEvent(2,'你好\n\n'+reminder+'\n\n'+notice),userEvent(3,reminder),userEvent(4,'literal '+reminder),
    event(5,'inference.finish',{outcome:'success',stop_reason:'end_turn',duration_ms:12},{seq:2}),
    userEvent(6,'x'.repeat(400))]));
  const timeline=v.element('#timeline'), [hook,user,system,literal,finish,long]=timeline.children;
  const byClass=(row,name)=>descendants(row).find(node=>node.className?.split(' ').includes(name));
  assert.equal(byClass(hook,'step-label').textContent,'Hook · SessionStart');
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
  v.showDetail(0);assert.ok(detail.textContent.includes(command));
  v.showDetail(1);assert.ok(detail.textContent.includes(reminder));
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
  assert.match(expand(detail,'System prompt and tools').textContent,/admitted system/);
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
  assert.match(v.element('#timeline').children[3].textContent,/Tool finished · EditSuccess/);
  assert.ok(detail.textContent.includes(input));
  assert.match(detail.textContent,/Duration0 ms.*File changes.*Model-facing result.*File updated/s);
  const diff=descendants(detail).filter(el=>el.className?.startsWith('diff-'));
  assert.deepEqual(diff.map(el=>[el.className,el.textContent.trim()]),[['diff-header','@@ -1 +1 @@'],['diff-remove','-before'],['diff-add','+after']]);
  await done;
  assert.equal(v.requests.length,0,'tool execution renders without context reconstruction');
  v.showDetail(1);
  assert.match(detail.textContent,/Permission decision.*Authorityuser.*Allow, Deny.*Apply edit\?.*Requested invocation/s);
});
test('Bash and Agent show recorded details without inventing output',async()=>{
  const v=await viewer();v.openSession('native');
  v.ingestBatch(batch(1,[event(1,'tool.finish',{call_id:'bash',outcome:'failure',details:{lineCount:8,error:'exit 1',tail:'last line',tailTruncated:true}}),
    event(2,'tool.finish',{call_id:'agent',outcome:'success',child_session:'child',details:{Identity:{Name:'review'},Response:'review result'}}),
    event(3,'tool.finish',{call_id:'background',outcome:'success',details:{lineCount:0,taskId:'work'}})]));
  const detail=v.element('#detail-body');
  v.showDetail(0);assert.match(detail.textContent,/Shell output.*exit 1.*Recorded tail \(truncated\).*last line/s);
  v.showDetail(1);assert.match(detail.textContent,/Child sessionchild.*Agent result.*review result/s);
  v.showDetail(2);assert.match(detail.textContent,/Background taskwork/);assert.doesNotMatch(detail.textContent,/Recorded output/);
});
test('unknown and malformed events stay visible as inert raw text',async()=>{
  const v=await viewer();v.openSession('future');
  v.ingestBatch(batch(1,[event(1,'future.event',{text:'<img src=x onerror=alert(1)>'}),null]));
  assert.equal(v.element('#timeline').children.length,2);
  assert.match(v.element('#timeline').children[0].textContent,/Unsupported \(raw\)/);
  await v.showDetail(0);
  assert.equal(v.requests.length,0);
  const detail=v.element('#detail-body');
  assert.ok(detail.textContent.includes('<img src=x onerror=alert(1)>'));
  assert.ok(descendants(detail).every(node=>node.innerHTML===''));
});
test('diagnostic raw bytes are bounded server data rendered as text, not HTML',async()=>{
  const v=await viewer();v.openSession('broken');const pending=v.showDiagnostics('broken',v.state.eventSource);
  v.requests[0].resolve({error:'hash mismatch',unvalidatedText:'<script>bad()</script>'});await pending;
  assert.equal(v.element('#detail-body').children.at(-1).textContent,'<script>bad()</script>');
  assert.equal(v.element('#detail-body').children.at(-1).innerHTML,'');
});
test('exact native wire JSON preserves numbers beyond JavaScript precision',async()=>{
  const v=await viewer();v.openSession('exact');
  const wire=JSON.stringify(batch(1,[event(1,'inference.start',{sampling_params:{seed:'NUMBER'}})])).replace('"NUMBER"','9007199254740993');
  v.state.eventSource.onmessage({data:wire});
  const pending=v.showDetail(0);
  const exact='{"context":{"System":"native","SamplingParams":{"seed":9007199254740993}},"request":{"System":"native","SamplingParams":{"seed":9007199254740993}},"history":[]}';
  v.requests[0].resolveText(exact);await pending;
  const detail=v.element('#detail-body');
  const technical=expand(detail,'Technical details');
  assert.equal(expand(technical,'Committed batch JSON').children.at(-1).textContent,wire);
  const raw=expand(technical,'Exact snapshot JSON');
  await new Promise(setImmediate);
  assert.equal(raw.children.at(-1).children.at(-1).textContent,exact);
  assert.equal(v.requests.length,1);
});
test('Markdown links cannot execute transcript content',async()=>{
  const v=await viewer();
  for(const url of ['javascript:alert%281%29','JaVaScRiPt:alert%281%29','data:text/html,hello','vbscript:msgbox','//evil.test','javascript&#58;alert','\u0000javascript:alert'])assert.ok(!v.renderInline(`[link](${url})`).includes('<a '),url);
  for(const url of ['https://example.com','http://localhost/','mailto:a@example.com'])assert.ok(v.renderInline(`[link](${url})`).includes('<a '),url);
  const html=v.renderInline('[<img src=x>](https://example.com/"onmouseover="bad)');
  assert.ok(!html.includes('<img'));assert.ok(html.includes('noopener noreferrer'));assert.ok(!html.includes('href="https://example.com/"'));
});
