/* Optional real-browser half of verify-rpc-agent.py. Synthetic accounts only. */
'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const {chromium}=require(process.env.M4_PLAYWRIGHT_MODULE||'playwright');
(async()=>{
 const base=process.env.M4_BROWSER_URL,output=process.env.M4_BROWSER_OUTPUT;
 const browser=await chromium.launch({headless:true,...(process.env.M4_CHROMIUM?{executablePath:process.env.M4_CHROMIUM}:{})});
 const context=await browser.newContext({viewport:{width:1365,height:1000}}),page=await context.newPage(),errors=[];
 page.setDefaultTimeout(10000);
 page.on('pageerror',error=>errors.push(String(error)));
 const stats=async()=>{const response=await context.request.get(process.env.M4_BROWSER_FIXTURE+'/fixture/status');return response.json();};
 const currentTask=async()=>page.evaluate(async()=>{const r=await fetch('/api/agent/executions');return r.json();});
 const checks=[];
 try{
  await page.goto(base);
  await page.locator('#login [name=email]').fill(process.env.M4_BROWSER_EMAIL);
  await page.locator('#login [name=password]').fill(process.env.M4_BROWSER_PASSWORD);
  await page.locator('#auth-submit').click();
  await page.locator('#workspace').waitFor({state:'visible'});
  await page.locator('nav .nav-more summary').click();
  await page.locator('nav [data-page=agent]').click();
  if(process.env.M4_BROWSER_PAUSED_ID){
   await page.locator('#agent-narration-panel').waitFor({state:'visible'});
   await page.locator('[data-narration-continue]').click();
   const confirm=page.locator('[data-narration-confirm]'),checkbox=page.locator('[data-narration-unknown]');
   await checkbox.waitFor({state:'visible'});
   assert.equal(await checkbox.isChecked(),false);
   assert.equal(await confirm.isDisabled(),true);
   await checkbox.check();
   assert.equal(await confirm.isDisabled(),false);
   await page.locator('[data-narration-close]').click();
   checks.push('unknown-outcome-retry-requires-unchecked-explicit-acknowledgment');
  }
  await page.locator('#agent-skill-form button[type=submit]').click();
  const narrate=page.locator('#agent-execution-current [data-execution-narrate]');
  await narrate.waitFor();
  const before=(await stats()).calls;
  await narrate.click();
  await page.locator('#agent-narration-review').waitFor({state:'visible'});
  assert.match(await page.locator('[data-narration-preview]').textContent(),/UNTRUSTED DATA/);
  assert.match(await page.locator('[data-narration-review-status]').textContent(),/本地无模型测试/);
  assert.equal((await stats()).calls,before);
  await page.locator('[data-narration-close]').click();
  assert.equal((await stats()).calls,before);
  checks.push('review-dismissal-has-no-provider-call');
  await narrate.click();
  await page.locator('[data-narration-confirm]').click();
  await page.waitForFunction(()=>document.querySelector('[data-narration-status]')?.textContent==='正在生成说明'&&document.querySelector('[data-narration-text]')?.textContent.length>0);
  assert.equal(await page.locator('[data-narration-status]').textContent(),'正在生成说明');
  const child=(await currentTask()).find(v=>v.skill_id==='evidence-explanation');
  assert.ok(child);
  // Reload aborts the browser observer. Generation and SQL checkpoints survive.
  await page.reload();
  await page.locator('nav .nav-more summary').click();
  await page.locator('nav [data-page=agent]').click();
  await page.locator('#agent-narration-panel').waitFor({state:'visible'});
  await page.waitForFunction(()=>document.querySelector('[data-narration-status]')?.textContent==='说明已完成');
  assert.equal((await stats()).calls,before+1);
  const text=page.locator('[data-narration-text]');
  assert.match(await text.textContent(),/<img/);
  assert.equal(await text.locator('img').count(),0);
  assert.equal(await page.evaluate(()=>!!window.__narrationExecuted),false);
  await page.screenshot({path:path.join(output,'browser-completed.png'),fullPage:true});
  checks.push('partial-visible-before-completion','reload-reattaches-without-generation','HTML-output-is-literal-text');
  // Open the saved parent in history; no new read tools or task is necessary.
  await page.locator('.agent-history > summary').click();
  const historyNarrate=page.locator('#agent-execution-list [data-execution-narrate]').first();
  await historyNarrate.click();
  await page.locator('[data-narration-confirm]').click();
  await page.waitForFunction(()=>document.querySelector('[data-narration-status]')?.textContent==='正在生成说明'&&document.querySelector('[data-narration-text]')?.textContent.length>0);
  const cancelledBefore=(await stats()).canceled;
  await page.locator('[data-narration-stop]').click();
  await page.waitForFunction(()=>document.querySelector('[data-narration-status]')?.textContent==='已停止生成');
  assert.equal(await text.textContent(),'');
  for(let i=0;i<40&&(await stats()).canceled===cancelledBefore;i++)await new Promise(resolve=>setTimeout(resolve,50));
  assert.ok((await stats()).canceled>cancelledBefore);
  assert.equal((await stats()).calls,before+2);
  assert.deepEqual(errors,[]);
  checks.push('UI-cancel-fences-task-and-cancels-upstream');
  await page.screenshot({path:path.join(output,'browser-cancelled.png'),fullPage:true});
  fs.writeFileSync(path.join(output,'browser-result.json'),JSON.stringify({status:'PASS',backend:'fixture-no-model',transport:process.env.M4_BROWSER_TRANSPORT||'rpc',checks,page_errors:errors},null,2));
  console.log(JSON.stringify({status:'PASS',checks}));
 }catch(error){await page.screenshot({path:path.join(output,'browser-failure.png'),fullPage:true}).catch(()=>{});throw error;}
 finally{await context.close();await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
