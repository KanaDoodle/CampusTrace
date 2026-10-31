/* Real-browser local file parsing; synthetic documents, no external requests. */
'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),http=require('node:http'),path=require('node:path');
const {chromium}=require(process.env.CAMPUS_PLAYWRIGHT_MODULE||'playwright');
function pdf(pages=1){
 const text='BT /F1 12 Tf 40 700 Td (Golang Kafka example@example.com 13800138000) Tj ET';
 const objects=['<< /Type /Catalog /Pages 2 0 R >>',`<< /Type /Pages /Kids [${Array.from({length:pages},(_,i)=>`${i+5} 0 R`).join(' ')}] /Count ${pages} >>`,'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',`<< /Length ${text.length} >>\nstream\n${text}\nendstream`];
 for(let i=0;i<pages;i++)objects.push('<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents 4 0 R >>');
 let raw='%PDF-1.4\n';const offsets=[0];for(const [i,obj] of objects.entries()){offsets.push(Buffer.byteLength(raw));raw+=`${i+1} 0 obj\n${obj}\nendobj\n`;}
 const start=Buffer.byteLength(raw);raw+=`xref\n0 ${objects.length+1}\n0000000000 65535 f \n`+offsets.slice(1).map(n=>`${String(n).padStart(10,'0')} 00000 n \n`).join('')+`trailer\n<< /Size ${objects.length+1} /Root 1 0 R >>\nstartxref\n${start}\n%%EOF\n`;return [...Buffer.from(raw)];
}
(async()=>{
 const files=new Set(['/profile_local.js','/vendor/jszip.min.js','/vendor/pdf.min.mjs','/vendor/pdf.worker.min.mjs']);
 const server=http.createServer((req,res)=>{
  const name=new URL(req.url,'http://localhost').pathname;
  if(name==='/'){res.setHeader('Content-Type','text/html; charset=utf-8');res.end('<script src="/profile_local.js"></script><script src="/vendor/jszip.min.js"></script>');return;}
  if(!files.has(name)){res.writeHead(404);res.end();return;}
  res.setHeader('Content-Type','application/javascript; charset=utf-8');res.end(fs.readFileSync(path.join(__dirname,'../web',name)));
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));let browser;
 try{
  browser=await chromium.launch({headless:true,...(process.env.CAMPUS_CHROMIUM?{executablePath:process.env.CAMPUS_CHROMIUM}:{})});
  const context=await browser.newContext(),page=await context.newPage(),external=[];
  await context.route('**/*',route=>{if(new URL(route.request().url()).hostname==='127.0.0.1')return route.continue();external.push(route.request().url());return route.abort();});
  await page.goto(`http://127.0.0.1:${server.address().port}`);
  const checks=await page.evaluate(async({one,many})=>{
   const p=window.CampusProfileLocal,checks=[];
   const text=await p.readFile(new File([new Uint8Array(one)],'resume.pdf'));
   if(!text.includes('Golang Kafka')||text.includes('example@example.com')||text.includes('13800138000'))throw Error('PDF extraction or redaction failed');checks.push('PDF reader and worker extract and redact local text');
   const zip=new JSZip();zip.file('word/document.xml','<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Golang Kafka example@example.com 13800138000</w:t></w:r></w:p></w:body></w:document>');
   const docx=await p.readFile(new File([await zip.generateAsync({type:'uint8array'})],'resume.docx'));
   if(!docx.includes('Golang Kafka')||docx.includes('example@example.com')||docx.includes('13800138000'))throw Error('DOCX extraction or redaction failed');checks.push('DOCX archive and XML extraction preserve redaction');
   let limited=false;try{await p.readFile(new File([new Uint8Array(many)],'long.pdf'));}catch(e){limited=String(e).includes('30');}if(!limited)throw Error('PDF page limit lost');checks.push('PDF page limit retained');
   const noBody=new JSZip();noBody.file('readme.txt','synthetic fixture');
   let missing=false;try{await p.readFile(new File([await noBody.generateAsync({type:'uint8array'})],'empty.docx'));}catch(e){missing=String(e).includes('正文');if(!missing)throw new Error('unexpected DOCX rejection: '+String(e));}if(!missing)throw Error('missing DOCX body accepted');checks.push('DOCX missing body rejected');
   return checks;
  },{one:pdf(),many:pdf(31)});
  assert.deepEqual(external,[]);checks.push('all document parsing requests stay on loopback');
  const report={status:'PASS',checks,external_requests:external.length};
  if(process.env.CAMPUS_VENDOR_REPORT)fs.writeFileSync(process.env.CAMPUS_VENDOR_REPORT,JSON.stringify(report,null,2)+'\n');
  console.log(JSON.stringify(report));
 }finally{if(browser)await browser.close();await new Promise(resolve=>server.close(resolve));}
})().catch(error=>{console.error(error);process.exitCode=1;});
