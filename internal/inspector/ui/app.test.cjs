const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');
class Element {
  constructor() {
    this.children=[];this.textContent='';this.innerHTML='';this.dataset={};this.listeners={};
    const classes=new Set();
    this.classList={add:n=>classes.add(n),remove:n=>classes.delete(n),contains:n=>classes.has(n),toggle:(n,on)=>on?classes.add(n):classes.delete(n)};
  }
  addEventListener(event,handler){this.listeners[event]=handler;}
  appendChild(child){this.children.push(child);}
  replaceChildren(...children){this.children=children;}
}
async function viewer(){
  const elements=new Map(), requests=[];
  const element=id=>{if(!elements.has(id))elements.set(id,new Element());return elements.get(id);};
  const context=vm.createContext({URL,document:{querySelector:element,createElement:()=>new Element()},
    EventSource:class{constructor(){this.listeners={};}close(){this.closed=true;}addEventListener(event,handler){this.listeners[event]=handler;}},
    setInterval(){},fetch:url=>{
      if(url==='/api/sessions')return Promise.resolve({ok:true,json:async()=>[]});
      return new Promise((resolve,reject)=>requests.push({url,reject,resolve:body=>resolve({ok:true,json:async()=>body}),resolveText:text=>resolve({ok:true,text:async()=>text})}));
    }});
  vm.runInContext(readFileSync(join(__dirname,'assets/app.js'),'utf8')+'\nglobalThis.viewer={state,openSession,showDetail,refreshPromptPanel,renderInline,classify,passesFilter,ingestBatch,showDiagnostics};',context);
  await new Promise(setImmediate);return {...context.viewer,element,requests};
}
const event=(seq,type,payload={},source)=>({seq,type,v:1,time:1000,payload,...(source?{source}:{})});
const batch=(sequence,events)=>({tail:{sequence,hash:'hash'+sequence,count:sequence},batch:{id:'batch'+sequence,records:[{type:'app',name:'san.events',data:{version:1,events,validation:{}}}]}});
const snapshot=text=>({context:{system:text,tools:[]},history:[]});

test('late detail responses cannot overwrite another record or session',async()=>{
  for(const change of ['record','session'])for(const fail of [false,true]){
    const v=await viewer();v.openSession('first');v.ingestBatch(batch(1,[event(1,'inference.start'),event(2,'message.append')]));
    const pending=v.showDetail(0);
    if(change==='session')v.openSession('second');else v.showDetail(1);
    const detail=v.element('#detail-body'),before=detail.children;
    // Prompt and detail have independent asynchronous fencing.
    const request=v.requests.find(r=>r.url.endsWith('/state/1'));
    if(fail)request.reject(new Error('stale failure'));else request.resolve(snapshot('obsolete'));
    v.requests[1].resolve(snapshot('obsolete'));
    await pending;await new Promise(setImmediate);
    assert.equal(detail.children,before,`${change} failure=${fail}`);
  }
});
test('late prompt responses cannot overwrite the selected record',async()=>{
  for(const fail of [false,true]){
    const v=await viewer();v.openSession('first');v.state.selectedRecordID='old';
    const pending=v.refreshPromptPanel(event(1,'message.append'));
    v.state.selectedRecordID='new';const current=v.refreshPromptPanel(event(2,'message.append'));
    v.requests[1].resolve(snapshot('current'));await current;
    const body=v.element('#prompt-body'),children=body.children;
    if(fail)v.requests[0].reject(new Error('old failed'));else v.requests[0].resolve(snapshot('obsolete'));
    await pending;assert.equal(body.children,children);
  }
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
  assert.match(v.element('#timeline').children[12].textContent,/#13 annotation.add ← child#7/);
  for(const type of types)assert.notEqual(v.classify(type),'unknown',type);
  v.state.filters.state=false;assert.equal(v.passesFilter(event(1,'session.update')),false);
});
test('acceptance and consumption remain distinct facts while native history stays deduplicated',async()=>{
  const v=await viewer();v.openSession('native');
  const queued={id:'queued',role:'user',content:[{type:'text',text:'next prompt'}]};
  const accepted=batch(1,[event(4,'message.append',{messages:[queued],accepted:true})]);
  const consumed=batch(2,[event(7,'message.append',{messages:[queued]},{seq:4})]);
  v.ingestBatch(accepted);v.ingestBatch(consumed);v.ingestBatch(accepted);v.ingestBatch(consumed);
  assert.equal(v.state.records.length,2);
  assert.notEqual(v.state.records[0]._key,v.state.records[1]._key);
  assert.match(v.element('#timeline').children[0].textContent,/#4 message.append.*accepted.*user:queued/);
  assert.match(v.element('#timeline').children[1].textContent,/#7 message.append ← local#4/);
  const pending=v.showDetail(1);
  const native={context:{messages:[queued]},history:[queued]};
  v.requests[0].resolve(native);v.requests[1].resolve(native);await pending;
  const children=v.element('#detail-body').children;
  const heading=children.findIndex(node=>node.textContent.startsWith('Full committed message history'));
  assert.equal(JSON.parse(children[heading+1].textContent).length,1);
  assert.equal(JSON.parse(children[heading+1].textContent)[0].id,'queued');
});
test('unknown and malformed events stay visible as inert raw text',async()=>{
  const v=await viewer();v.openSession('future');
  v.ingestBatch(batch(1,[event(1,'future.event',{text:'<img src=x onerror=alert(1)>'}),null]));
  assert.equal(v.element('#timeline').children.length,2);
  assert.match(v.element('#timeline').children[0].textContent,/unsupported \(raw\)/);
  const pending=v.showDetail(0);v.requests[0].reject(new Error('unsupported event'));v.requests[1].reject(new Error('unsupported event'));await pending;
  const children=v.element('#detail-body').children;
  assert.ok(children.some(node=>node.textContent.includes('<img src=x onerror=alert(1)>')));
  assert.ok(children.every(node=>node.innerHTML===''));
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
  const exact='{"context":{"system":"native","sampling_params":{"seed":9007199254740993}},"request":{"system":"native","sampling_params":{"seed":9007199254740993}},"history":[]}';
  v.requests[0].resolveText(exact);v.requests[1].resolveText(exact);await pending;
  const blocks=v.element('#detail-body').children.map(n=>n.textContent);
  assert.ok(blocks.includes(wire));assert.ok(blocks.includes(exact));
});
test('Markdown links cannot execute transcript content',async()=>{
  const v=await viewer();
  for(const url of ['javascript:alert%281%29','JaVaScRiPt:alert%281%29','data:text/html,hello','vbscript:msgbox','//evil.test','javascript&#58;alert','\u0000javascript:alert'])assert.ok(!v.renderInline(`[link](${url})`).includes('<a '),url);
  for(const url of ['https://example.com','http://localhost/','mailto:a@example.com'])assert.ok(v.renderInline(`[link](${url})`).includes('<a '),url);
  const html=v.renderInline('[<img src=x>](https://example.com/"onmouseover="bad)');
  assert.ok(!html.includes('<img'));assert.ok(html.includes('noopener noreferrer'));assert.ok(!html.includes('href="https://example.com/"'));
});
