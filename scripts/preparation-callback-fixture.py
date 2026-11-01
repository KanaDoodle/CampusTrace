#!/usr/bin/env python3
"""Acceptance-only loopback proxy: lose one returned Go model-step response."""
import http.server, json, socket, threading, urllib.request, os
lock=threading.Lock();dropped=False
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_POST(self):
  global dropped
  body=self.rfile.read(int(self.headers['Content-Length']))
  req=urllib.request.Request(os.environ['PREP_CALLBACK_TARGET']+self.path,data=body,headers={'Content-Type':'application/json','Authorization':self.headers.get('Authorization','')})
  with urllib.request.urlopen(req,timeout=36) as response:raw=response.read();status=response.status
  action=json.loads(body)['action']
  with lock:
   drop=not dropped and action=='model'
   if drop:dropped=True
  if drop:
   # Go has committed the returned result before this response is discarded.
   self.connection.shutdown(socket.SHUT_RDWR);self.connection.close();return
  self.send_response(status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
print(json.dumps({'event':'ready','address':'127.0.0.1:'+str(server.server_port)}),flush=True)
server.serve_forever()
