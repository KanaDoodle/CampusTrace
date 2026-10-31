#!/usr/bin/env python3
"""Summarize raw paired runs and trace/profile evidence without hiding errors."""
import argparse
from collections import defaultdict
import json
from pathlib import Path
import statistics
import subprocess


def values(summary, name):
    return summary['metrics'].get(name, {}).get('values', {})


def summarize(report):
    rows=[]
    for label,summary in report['phases'].items():
        if label.startswith('diagnostic-'): continue
        for kind in ('common','sparse','miss'):
            metric=values(summary,kind+'_ms')
            rows.append({'run':label,'class':kind,
                'attempts':values(summary,kind+'_attempts').get('count',0),
                'failures':values(summary,kind+'_failures').get('count',0),
                'p50_ms':metric.get('med'), 'p95_ms':metric.get('p(95)'), 'p99_ms':metric.get('p(99)'),
                'dropped':values(summary,'dropped_iterations').get('count',0),
                'k6_exit':summary['exit_code']})
    return rows


def quantiles(ms):
    ms=sorted(ms)
    return {'count':len(ms),'p50':statistics.median(ms),'p95':ms[min(len(ms)-1,int((len(ms)-1)*.95))],'max':max(ms)}


def traces(path):
    rows=[json.loads(line) for line in path.read_text().splitlines()]
    nodes={(r['trace_id'],r['span_id']):r for r in rows}
    children=defaultdict(list)
    groups=defaultdict(list)
    for row in rows:
        children[(row['trace_id'],row['parent_id'])].append(row)
        groups[(row.get('instance',''),row['service'],row['name'])].append(row['duration_ms'])
    operations=[]
    for (instance,service,name),ms in sorted(groups.items()):
        q=quantiles(ms)
        operations.append({'instance':instance,'service':service,'operation':name,'count':len(ms),
            'p50_ms':q['p50'],'p95_ms':q['p95'],'total_ms':sum(ms)})
    pages=defaultdict(list)
    remote=defaultdict(list)
    for row in rows:
        descendants=children[(row['trace_id'],row['span_id'])]
        if row['name']=='search.fallback':
            pages[row.get('instance','')].append(sum(c['name']=='search.fallback.page' for c in descendants))
        if row['name']=='POST /v1/query':
            calls=sorted([c for c in descendants if c['name']=='search.index'],key=lambda c:c['start_ns'])
            if len(calls)==3:
                for operation,c in zip(('resolve_alias','check_generation','query'),calls):
                    remote[(row.get('instance',''),operation)].append(c['duration_ms'])
    # Direct parent IDs verify propagation between the two services without
    # retaining request/query payloads in the trace collector.
    remote_roots=[r for r in rows if r['name']=='POST /v1/query']
    joined=sum(nodes.get((r['trace_id'],r['parent_id']),{}).get('name')=='search.http' for r in remote_roots)
    return {'operations':operations,'fallback_pages':{k:quantiles(v) for k,v in pages.items()},
        'remote_round_trips':[{'instance':k[0],'operation':k[1],**quantiles(v)} for k,v in sorted(remote.items())],
        'cross_service_query_spans':len(remote_roots),'joined_to_core_http':joined}


def profiles(directory):
    for profile in sorted(directory.glob('*-cpu.pb.gz')):
        result=subprocess.run(['go','tool','pprof','-top','-nodecount=30',str(profile)],capture_output=True,text=True,check=True)
        profile.with_suffix('').with_suffix('.top.txt').write_text(result.stdout+result.stderr)
    for after in sorted(directory.glob('*-allocs-after.pb.gz')):
        before=after.with_name(after.name.replace('-after','-before'))
        result=subprocess.run(['go','tool','pprof','-top','-sample_index=alloc_space','-nodecount=30',
            '-base',str(before),str(after)],capture_output=True,text=True,check=True)
        after.with_suffix('').with_suffix('.top.txt').write_text(result.stdout+result.stderr)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directories',type=Path,nargs='+')
    args=parser.parse_args()
    for directory in args.directories:
        report=json.loads((directory/'report.json').read_text())
        result={'jobs':report['seeded_jobs'],'source':report['source'],
            'candidate_source':report.get('candidate_source'),'fixture_sha256':report['fixture_sha256'],
            'environment':report['environment'],'rows':summarize(report)}
        if (directory/'traces.jsonl').is_file(): result['traces']=traces(directory/'traces.jsonl')
        (directory/'analysis.json').write_text(json.dumps(result,indent=2)+'\n')
        profiles(directory)
        print(directory, 'rows',len(result['rows']))


if __name__=='__main__': main()
