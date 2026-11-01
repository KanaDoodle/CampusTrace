#!/usr/bin/env python3
"""Loopback-only scripted Chat Completions responses. No model or cloud calls."""
import http.server,json,threading,time,uuid,socket
from project_defense_fixture import respond as defense_respond
lock=threading.Lock();state={'calls':0,'active':0,'mode':'normal','canceled':0}
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def send(self,code,value):
  raw=json.dumps(value,ensure_ascii=False).encode();self.send_response(code);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
 def do_GET(self):
  with lock:value=dict(state)
  self.send(200,value)
 def do_POST(self):
  raw=self.rfile.read(int(self.headers.get('Content-Length','0')));body=json.loads(raw)
  if self.path=='/fixture/mode':
   with lock:state['mode']=body['mode']
   self.send(200,{'ok':True});return
  if self.path!='/v1/chat/completions' or self.headers.get('Authorization')!='Bearer preparation-fixture-key':self.send(400,{});return
  with lock:state['calls']+=1;state['active']+=1;mode=state['mode']
  try:
   if mode=='fail':self.send(503,{'error':'controlled failure'});return
   if mode=='hang' or (mode=='hang-after-read' and len(body.get('messages',[]))>2):
    self.connection.settimeout(.1)
    while True:
     try:
      if self.connection.recv(1,socket.MSG_PEEK)==b'':
       with lock:state['canceled']+=1
       return
     except socket.timeout:pass
     except OSError:return
   quality=any(t['function']['name']=='finish_preparation_plan' and 'workload' in t['function']['parameters']['properties']['items']['items']['properties'] for t in body.get('tools',[]))
   messages=body['messages'];data=json.loads(messages[1]['content']);goal=data['goal'];sources=data['sources'];old=data.get('previous_plan')
   def reply(name,args):
    self.send(200,{'choices':[{'index':0,'finish_reason':'tool_calls','message':{'role':'assistant','content':None,'tool_calls':[{'id':'call_'+uuid.uuid4().hex,'type':'function','function':{'name':name,'arguments':json.dumps(args,ensure_ascii=False)}}]}}],'usage':{'prompt_tokens':150,'completion_tokens':100,'total_tokens':250}})
   if data.get('defense'):
    defense_respond(data,messages,reply,mode);return
   if not goal.get('minutes') or not goal.get('days'):reply('ask_preparation_question',{'question':'你还可以准备多少分钟、几天内完成？'});return
   if len(messages)==2 and not old:reply('search_preparation_knowledge',dict({'query':'Go 并发 MySQL 索引'},**({'gap':'缺少复习核对点','expected_use':'用于口述自查'} if quality else {})));return
   if old:
    excluded=set(goal.get('mastered',[])+goal.get('completed',[]));items=[dict(v) for v in old['items'] if v['id'] not in excluded]
   else:
    jd=next((s for s in sources if s['kind']=='JD'),None)
    if not jd:reply('ask_preparation_question',{'question':'请核对这次申请原始 JD，缺少历史快照时不能使用当前材料替代。'});return
    items=[]
    for key,title in [('go','Go 并发与取消'),('mysql','MySQL 索引与事务'),('project','项目实现与取舍')]:
     items.append({'id':key,'title':title,'minutes':60,'day':1,'basis':'JD' if key!='project' else 'GENERAL','action':'按冻结材料解释实现、约束，并做一次口头演练','deliverable':'一份实现与故障处理提纲','references':[{'source_id':jd['id'],'quote':jd['text'].split('\n')[0]}] if key!='project' else []})
   for item in items:item['minutes']=goal['minutes']//max(1,len(items))
   if quality:
    definition=next(t['function'] for t in body.get('tools',[]) if t['function']['name']=='finish_preparation_plan')
    items=items[:definition['parameters']['properties']['items']['maxItems']]
    for item in items:
     item['minutes']=goal['minutes']//max(1,len(items))
     item['workload']={'mode':'ORAL','units':1,'scope':'1个问题：已有主题的口述与核对提纲'}
     for ref in item.get('references',[]):ref['supports']='原文要求该主题，支持本次口述重点'
   reply('finish_preparation_plan' ,{'summary':'结合留档材料，在剩余时间内完成优先复习和回答演练。','items':items})
  finally:
   with lock:state['active']-=1
server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
print(json.dumps({'event':'ready','address':'127.0.0.1:'+str(server.server_port),'backend':'controlled-response-no-model'}),flush=True)
server.serve_forever()
