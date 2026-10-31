import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from report import PHASES, compare, validate


def sample():
    report = {'schema_version':1,'workload':{'jobs':200,'query_rate':10,'write_rate':1,'seconds':15},
              'environment':{'host':'same runner','k6':'pinned'},'phases':{}}
    for name in PHASES:
        count = 150
        writes = 15 if name == 'mixed' else 0
        metrics = {key:{'values':{'count':value}} for key,value in {
            'query_requests':count,'query_failures':0,'mode_fulltext':count if name=='fulltext' else 0,
            'mode_basic':0 if name=='fulltext' else count,'dropped_iterations':0,'write_requests':writes,
            'write_failures':0,'iterations':count+writes,'http_reqs':count+writes}.items()}
        metrics.update(checks={'values':{'rate':1}},http_req_failed={'values':{'rate':0}},query_ms={'values':{'p(95)':40,'p(99)':60}})
        report['phases'][name] = {'metrics':metrics,'exit_code':0,'rebuild_injected':name=='rebuild',
                                 'latest_write_visible':name=='mixed','recovered_fulltext':True}
    return report


class GateTests(unittest.TestCase):
    def test_same_environment_and_small_variation_pass(self):
        baseline = sample()
        candidate = copy.deepcopy(baseline)
        candidate['phases']['fulltext']['metrics']['query_ms']['values']['p(95)'] = 55
        errors, comparisons = compare(candidate,baseline)
        self.assertEqual(errors,[])
        self.assertEqual(len(comparisons),4)

    def test_online_rebuild_requires_fulltext_and_real_cutover(self):
        online = sample()
        phase = online['phases']['rebuild']
        phase.update(rebuild_mode='online',rebuild_completed=True,rebuild_source_generation='old:uuid',rebuild_target_generation='new:uuid')
        phase['metrics']['mode_basic']['values']['count'] = 0
        phase['metrics']['mode_fulltext']['values']['count'] = 150
        self.assertEqual(validate(online), [])
        self.assertEqual(compare(online,sample())[0], [])
        for change in ('fallback','unfinished','same_generation','missing_injection','unknown'):
            with self.subTest(change=change):
                bad=copy.deepcopy(online);phase=bad['phases']['rebuild']
                if change=='fallback':phase['metrics']['mode_basic']['values']['count']=1
                if change=='unfinished':phase['rebuild_completed']=False
                if change=='same_generation':phase['rebuild_target_generation']='old:uuid'
                if change=='missing_injection':phase['rebuild_injected']=False
                if change=='unknown':phase['rebuild_mode']='unsupported'
                self.assertTrue(validate(bad))

    def test_zero_samples_missing_phases_and_wrong_modes_fail(self):
        for mutation in ('missing','zero','mode','recovery','metric','interrupted'):
            with self.subTest(mutation=mutation):
                report = sample()
                if mutation=='missing': del report['phases']['mixed']
                if mutation=='zero': report['phases']['fulltext']['metrics']['query_requests']['values']['count']=0
                if mutation=='mode': report['phases']['fulltext']['metrics']['mode_basic']['values']['count']=1
                if mutation=='recovery': report['phases']['rebuild']['recovered_fulltext']=False
                if mutation=='metric': del report['phases']['fulltext']['metrics']['query_failures']
                if mutation=='interrupted': report['phases']['fulltext']['metrics']['iterations']['values']['count']=149
                self.assertTrue(validate(report))

    def test_generator_saturation_and_missing_update_fail(self):
        report=sample();report['phases']['mixed']['metrics']['dropped_iterations']['values']['count']=2
        self.assertTrue(validate(report))
        report=sample();report['phases']['mixed']['latest_write_visible']=False
        self.assertTrue(validate(report))

    def test_cross_machine_or_workload_is_not_a_pass(self):
        for field in ('environment','workload'):
            baseline=sample();candidate=sample();candidate[field]['different']=True
            errors,comparison=compare(candidate,baseline)
            self.assertTrue(errors);self.assertEqual(comparison,[])

    def test_cli_returns_failure_on_latency_regression(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);baseline=sample();candidate=sample()
            candidate['phases']['fulltext']['metrics']['query_ms']['values']['p(95)']=120
            (root/'base.json').write_text(json.dumps(baseline));(root/'candidate.json').write_text(json.dumps(candidate))
            result=subprocess.run([sys.executable,str(Path(__file__).with_name('report.py')),str(root/'candidate.json'),'--baseline',str(root/'base.json')],capture_output=True,text=True)
            self.assertEqual(result.returncode,1)
            self.assertIn('p(95) regression',result.stdout)

    def test_transient_mode_mix_does_not_gate_latency(self):
        candidate=sample();baseline=sample()
        candidate['phases']['rebuild']['metrics']['query_ms']['values']['p(95)']=2000
        self.assertEqual(compare(candidate,baseline)[0],[])

    def test_small_samples_and_nonfinite_quantiles_do_not_pass(self):
        candidate=sample();baseline=sample()
        for report in (candidate,baseline):
            report['workload'].update(seconds=10,query_rate=5)
            for phase in report['phases'].values():
                phase['metrics']['query_requests']['values']['count']=50
                phase['metrics']['mode_fulltext']['values']['count']=50 if phase is report['phases']['fulltext'] else 0
                phase['metrics']['mode_basic']['values']['count']=0 if phase is report['phases']['fulltext'] else 50
                total=50+phase['metrics']['write_requests']['values']['count']
                phase['metrics']['iterations']['values']['count']=total;phase['metrics']['http_reqs']['values']['count']=total
        self.assertTrue(any('100 samples' in error for error in compare(candidate,baseline)[0]))
        candidate=sample();candidate['phases']['fulltext']['metrics']['query_ms']['values']['p(95)']=float('nan')
        with self.assertRaises(ValueError):validate(candidate)


if __name__=='__main__':unittest.main()
