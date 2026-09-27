'use strict';
(function(root) {
  const patterns = [
    [/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi, '[已移除邮箱]'],
    [/(?:https?:\/\/|www\.|github\.com\/|linkedin\.com\/)\S+/gi, '[已移除链接]'],
    [/\b1[3-9]\d{9}\b/g, '[已移除电话]'],
    [/\b\d{17}[\dXx]\b/g, '[已移除证件号码]'],
    [/(?:姓名|真实姓名|现居住地|家庭住址|通讯地址|联系地址|身份证号|联系电话|手机号码)\s*[:：]\s*[^\n\r]+/g, '[已移除身份或联系信息]']
  ];
  function redact(text) {
    let result=String(text||'');
    for(const [pattern,replacement] of patterns) result=result.replace(pattern,replacement);
    return result;
  }
  function hasDirectIdentifiers(text) {
    return patterns.some(([pattern])=>{pattern.lastIndex=0;return pattern.test(text);});
  }
  async function readDocx(file) {
    if(!root.JSZip) throw new Error('文档读取组件未就绪，请刷新页面重试。');
    const zip=await root.JSZip.loadAsync(await file.arrayBuffer());
    const part=zip.file('word/document.xml');
    if(!part) throw new Error('无法读取 DOCX 正文。');
    const xml=await part.async('string');
    if(xml.length>400000) throw new Error('简历正文过长，请改用较短的文本文件。');
    const doc=new DOMParser().parseFromString(xml,'application/xml');
    if(doc.querySelector('parsererror')) throw new Error('DOCX 正文格式无法解析。');
    const paragraphs=[...doc.getElementsByTagName('w:p')];
    return paragraphs.map(p=>[...p.getElementsByTagName('w:t')].map(t=>t.textContent).join('')).filter(Boolean).join('\n');
  }
  async function readPdf(file) {
    const pdfjs=await import('/vendor/pdf.min.mjs');
    pdfjs.GlobalWorkerOptions.workerSrc='/vendor/pdf.worker.min.mjs';
    const loading=pdfjs.getDocument({data:new Uint8Array(await file.arrayBuffer())});
    let pdf;
    try {
      pdf=await loading.promise;
      if(pdf.numPages>30) throw new Error('PDF 超过 30 页，请先精简简历。');
      const pages=[];
      for(let n=1;n<=pdf.numPages;n++) {
        const content=await (await pdf.getPage(n)).getTextContent();
        pages.push(content.items.map(item=>item.str+(item.hasEOL?'\n':' ')).join('').trim());
      }
      return pages.join('\n');
    } finally { await loading.destroy(); }
  }
  async function readFile(file) {
    if(!file || file.size>5*1024*1024) throw new Error('请选择不超过 5 MB 的简历。');
    const name=file.name.toLowerCase();
    let text;
    if(name.endsWith('.txt')) text=await file.text();
    else if(name.endsWith('.docx')) text=await readDocx(file);
    else if(name.endsWith('.pdf')) text=await readPdf(file);
    else throw new Error('支持 PDF、DOCX 和 TXT 简历。');
    text=text.replace(/\r\n?/g,'\n').trim();
    if(!text) throw new Error('没有读到可用文字。扫描版 PDF 暂不支持，请使用文字版简历。');
    if(text.length>100000) throw new Error('简历正文过长，请先精简文件。');
    return redact(text);
  }
  const api={redact,hasDirectIdentifiers,readFile};
  if(typeof module==='object'&&module.exports) module.exports=api;
  root.CampusProfileLocal=api;
})(typeof window==='undefined'?globalThis:window);
