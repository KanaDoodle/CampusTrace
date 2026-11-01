#!/usr/bin/env python3
"""Create or validate a separate, attributable semantic review; never invent scores."""
import argparse, hashlib, json, pathlib
DIMENSIONS=['target_requirement_coverage','citation_entailment','feasible_workload','feedback_compliance','no_fabricated_experience','retrieval_added_value']

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--report',type=pathlib.Path,required=True);p.add_argument('--output',type=pathlib.Path);p.add_argument('--validate',type=pathlib.Path);args=p.parse_args()
 raw=args.report.read_bytes();report=json.loads(raw);digest=hashlib.sha256(raw).hexdigest()
 if args.validate:
  review=json.loads(args.validate.read_bytes());assert review['report_sha256']==digest,'report changed'
  expected={(r['case_id'],r['arm']) for r in report['results']};seen=set()
  for row in review['reviews']:
   key=(row['case_id'],row['arm']);assert key in expected and key not in seen;seen.add(key)
   for dimension in DIMENSIONS:
    cell=row['scores'][dimension];score=cell['score'];assert score in [None,0,1,2,'N/A']
    if score is not None:assert row['reviewer'].strip() and cell['evidence'].strip(),'scored claims need reviewer and evidence'
  assert seen==expected,'missing case/arm reviews';print('Review structure and report binding valid; scores remain reviewer judgments.');return
 if not args.output:raise SystemExit('--output or --validate required')
 rows=[]
 for r in report['results']:
  rows.append({'case_id':r['case_id'],'arm':r['arm'],'program_expectation_met':r['program_expectation_met'],'reviewer':'','reviewer_kind':'UNREVIEWED','scores':{d:{'score':None,'evidence':''} for d in DIMENSIONS},'notes':'For interrupted tasks judge the actual question/failure; N/A where no plan exists. A literal citation or newly retrieved source alone does not establish semantic support or usefulness.'})
 output={'report_sha256':digest,'evidence_kind':report['mode'],'scale':'0=violated, 1=partial, 2=meets, N/A=not applicable; null=not reviewed','reviews':rows}
 with args.output.open('x',encoding='utf-8') as f:json.dump(output,f,ensure_ascii=False,indent=2);f.write('\n')
 args.output.chmod(0o600);print(args.output)
if __name__=='__main__':main()
