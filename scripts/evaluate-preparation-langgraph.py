#!/usr/bin/env python3
"""Controlled three-arm evaluation through the actual Python graph. No real API calls."""
import argparse, json, os, pathlib, socket, subprocess, time, urllib.request

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--output',type=pathlib.Path,required=True);p.add_argument('--dataset',default='internal/agent/testdata/preparation-tasks.json');p.add_argument('--arms',default='single-prompt,fixed-plus-synthesis,dynamic');p.add_argument('--splits',choices=['both','development','validation'],default='both');p.add_argument('--defense-strategy',type=pathlib.Path);args=p.parse_args()
    work=args.output.resolve();work.mkdir(parents=True,exist_ok=True)
    if any(work.iterdir()):raise SystemExit('use a new empty output directory')
    root=pathlib.Path(__file__).resolve().parents[1]
    def port():
        with socket.socket() as sock:sock.bind(('127.0.0.1',0));return sock.getsockname()[1]
    gp,cp=port(),port();url=f'http://127.0.0.1:{gp}';env=dict(os.environ,GOWORK='off',LLM_URL='',LLM_API_KEY='',LLM_MODEL='',PREPARATION_GO_URL=f'http://127.0.0.1:{cp}',PREPARATION_CHECKPOINT_DB=str(work/'checkpoints.sqlite'),LANGSMITH_TRACING='false')
    with (work/'service.log').open('w') as log:
        proc=subprocess.Popen([str(root/'services/preparation/.venv/bin/python'),'-m','uvicorn','app:app','--app-dir',str(root/'services/preparation'),'--host','127.0.0.1','--port',str(gp),'--no-access-log'],cwd=root,env=env,stdout=log,stderr=log)
        try:
            for _ in range(150):
                if proc.poll() is not None:raise RuntimeError('Python exited; inspect service.log')
                try:
                    urllib.request.urlopen(url+'/healthz',timeout=1).close();break
                except OSError:time.sleep(.1)
            else:raise RuntimeError('LangGraph not ready')
            splits=['development','validation'] if args.splits=='both' else [args.splits]
            for split in splits:
                subprocess.run(['go','run','./cmd/preparation-eval','-split',split,'-dataset',args.dataset,'-arms',args.arms,'-graph-url',url,'-graph-callback-addr',f'127.0.0.1:{cp}','-output',str(work/(split+'.json'))]+(['-defense-strategy',str(args.defense_strategy.resolve())] if args.defense_strategy else []),cwd=root,env=env,check=True)
            reports=[json.loads((work/(x+'.json')).read_text()) for x in splits]
            tasks=json.loads((root/args.dataset).read_text())
            if args.splits=='both' and any('expected_memory_ids' in t for t in tasks):
                results={r['case_id']:r for report in reports for r in report['results']}
                pairs=[]
                for task in tasks:
                    if not task['id'].endswith('-confirmed'):continue
                    key=task['id'][:-len('-confirmed')];on=results[task['id']];off=results[key+'-off']
                    summarize=lambda r:{'state':r['state'],'program_expectation_met':r['program_expectation_met'],'memory_ids':r.get('memory_ids',[]),'memory_context_bytes':r.get('memory_context_bytes',0),'model_calls':r['model_calls'],'tool_calls':r['tool_calls'],'next_question':(r.get('defense',{}).get('review') or {}).get('next_question'),'usage':r['usage'],'estimated_cost_usd':r['estimated_cost_usd']}
                    pairs.append({'case':key,'on':summarize(on),'off':summarize(off),'semantic_scores':{'feedback_correctness':None,'memory_added_value':None,'correction_compliance':None},'reviewer':'UNREVIEWED'})
                (work/'memory-pairs.json').write_text(json.dumps({'evidence':'controlled-model-responses-real-Go-Python-LangGraph','external_model_calls':0,'pairs':pairs,'quality_conclusion':'NOT_MEASURED; scripted branching is not a semantic or learning gain'},ensure_ascii=False,indent=2)+'\n')
            summary={'evidence':'controlled-model-responses-real-Go-Python-LangGraph','external_model_calls':0,'graph_version':'preparation-langgraph-v1','reports':{r['split']:r['summary'] for r in reports},'semantic_quality':'NOT_MEASURED; controlled responses do not prove model reasoning'}
            (work/'summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n');print(json.dumps(summary,ensure_ascii=False,indent=2))
        finally:
            proc.terminate()
            try:proc.wait(5)
            except subprocess.TimeoutExpired:proc.kill();proc.wait()
if __name__=='__main__':main()
