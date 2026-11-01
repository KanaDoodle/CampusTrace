'use strict';
const fs=require('node:fs'),path=require('node:path');
(async()=>{
 const {chromium}=require(process.env.PREP_PLAYWRIGHT_MODULE),browser=await chromium.launch({headless:true});
 try{
  const page=await browser.newPage({viewport:{width:1360,height:1000}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(process.env.PREP_BROWSER_URL);await page.locator('#login input[name=email]').fill(process.env.PREP_BROWSER_EMAIL);await page.locator('#login input[name=password]').fill(process.env.PREP_BROWSER_PASSWORD);await page.locator('#auth-submit').click();await page.locator('#workspace').waitFor({state:'visible'});
  await page.evaluate(async app=>{await page('dossier',{applicationID:app});},process.env.PREP_BROWSER_APPLICATION);
  await page.locator('#dossier-agent-plan').click();
  await page.getByRole('heading',{name:'把有限时间用在这次面试上'}).waitFor();await page.locator('.prep-result').waitFor();
  await page.locator('#prep-feedback select[name="status-go"]').selectOption('mastered');await page.locator('#prep-feedback input[name=minutes]').fill('60');await page.locator('#prep-feedback textarea').fill('Go 并发已经掌握，只剩一小时');
  await page.getByRole('button',{name:'核对下一版输入'}).click();await page.locator('#prep-review').getByText(/60 分钟/).waitFor();
  await page.locator('#prep-consent').check();await page.locator('#prep-start').click();await page.locator('.prep-result').getByText('第 2 版').waitFor();await page.locator('[data-prep-save]').waitFor();
  const titles=await page.locator('.prep-plan h3').allTextContents();if(titles.some(x=>x.includes('Go 并发')))throw Error('mastered item returned');
  const total=await page.locator('.prep-total').textContent();if(!total.includes('/ 60 分钟'))throw Error('time budget not applied');
  await page.locator('[data-prep-save]').click();await page.locator('#prep-confirm-save').click();await page.getByRole('heading',{name:'计划已生成 · 已保存',exact:true}).waitFor();
  await page.screenshot({path:path.join(process.env.PREP_BROWSER_OUTPUT,'preparation-plan.png'),fullPage:true});
  await page.reload();await page.locator('#workspace').waitFor();await page.evaluate(async app=>{await page('preparation',{applicationID:app});},process.env.PREP_BROWSER_APPLICATION);await page.getByRole('heading',{name:'计划已生成 · 已保存',exact:true}).waitFor();
  if(errors.length)throw Error(JSON.stringify(errors));fs.writeFileSync(path.join(process.env.PREP_BROWSER_OUTPUT,'browser.json'),JSON.stringify({status:'PASS',checks:['natural_entry','review_before_call','mastered_omitted','sixty_minute_budget','explicit_save','refresh_persisted','no_page_errors']},null,2));
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
