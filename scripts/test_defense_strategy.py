"""Contract tests use synthetic artifacts, not model-quality measurements."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('strategy', Path(__file__).with_name('defense-strategy.py'))
S = importlib.util.module_from_spec(spec)
spec.loader.exec_module(S)
DIMS = ['answer_feedback_correctness', 'citation_entailment', 'feasible_workload', 'feedback_compliance', 'no_fabricated_experience', 'memory_added_value', 'correction_compliance']


class StrategyTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.experiment = self.root / 'experiment'
        self.experiment.mkdir()
        self.base = dict(schema='defense-teaching-v1', explain='code-first', followup='current-gap', reading='gap-only')
        self.candidate = dict(self.base, followup='contrast')
        tasks = [{'id': 'dev', 'split': 'development'}, {'id': 'val', 'split': 'validation'}]
        S.write_new(self.experiment / 'dataset.json', tasks)
        S.write_new(self.experiment / 'baseline-profile.json', self.base)
        S.write_new(self.experiment / 'candidate-profile.json', self.candidate)
        self.m = dict(schema='defense-strategy-experiment-v1', code_sha256=S.code_sha(),
                      base_strategy_sha256=S.digest(S.canonical(self.base)), candidate_strategy_sha256=S.digest(S.canonical(self.candidate)),
                      dataset_sha256=S.digest((self.experiment / 'dataset.json').read_bytes()), reports={}, artifacts={}, mode='controlled-response-program-regression')
        for split, case in [('development', 'dev'), ('validation', 'val')]:
            for label, p in [('baseline', self.base), ('candidate', self.candidate)]:
                name = label + '-' + split + '.json'
                report = dict(split=split, dataset_sha256=self.m['dataset_sha256'], arms=['dynamic'], mode=self.m['mode'],
                              model_config={}, max_calls=24, per_task_model_limit=6, framework_versions={}, graph_version='test',
                              agent_version='test', tools_version='test', project_evidence_sha256='x' * 64, quality_version='test',
                              defense_prompt_version='test', defense_teaching_strategy=p, defense_teaching_strategy_sha256=S.digest(S.canonical(p)),
                              dynamic_executor='langgraph', external_model_attempts=0, semantic_rubric={'dimensions': DIMS},
                              results=[dict(case_id=case, arm='dynamic', program_expectation_met=True, state='COMPLETED', model_calls=2)])
                S.write_new(self.experiment / name, report)
                self.m['reports'][label + '-' + split] = name
                self.scores(label, split, None)
        self.freeze()

    def freeze(self):
        names = ['dataset.json', 'baseline-profile.json', 'candidate-profile.json', *self.m['reports'].values()]
        self.m['artifacts'] = {n: S.digest((self.experiment / n).read_bytes()) for n in names}
        (self.experiment / 'experiment.json').write_text(json.dumps(self.m))

    def scores(self, label, split, score):
        report = self.experiment / self.m['reports'][label + '-' + split] if label + '-' + split in self.m['reports'] else self.experiment / (label + '-' + split + '.json')
        case = 'dev' if split == 'development' else 'val'
        data = dict(report_sha256=S.digest(report.read_bytes()), reviews=[dict(case_id=case, arm='dynamic', reviewer='controlled-fixture' if score is not None else '', reviewer_kind='SYNTHETIC_CONTROL' if score is not None else 'UNREVIEWED', scores={d: dict(score=score, evidence='合成门槛测试夹具，不是人工审阅' if score is not None else '') for d in DIMS})])
        (self.experiment / (label + '-' + split + '-scores.json')).write_text(json.dumps(data))

    def scored(self, baseline=1, candidate=2):
        for split in S.SPLITS:
            self.scores('baseline', split, baseline)
            self.scores('candidate', split, candidate)

    def test_empty_reviews_are_pending_and_no_gain_is_preserved(self):
        self.assertEqual(S.review(self.experiment)['status'], 'PENDING_HUMAN_REVIEW')
        self.scored(1, 1)
        self.assertEqual(S.review(self.experiment)['status'], 'NO_GAIN')

    def test_semantic_regression_blocks_even_when_other_scores_improve(self):
        self.scored()
        p = self.experiment / 'candidate-validation-scores.json'
        d = S.load(p)
        d['reviews'][0]['scores']['citation_entailment']['score'] = 0
        p.write_text(json.dumps(d))
        decision = S.review(self.experiment)
        self.assertEqual(decision['status'], 'REJECTED_SEMANTIC_REGRESSION')
        self.assertEqual(len(decision['semantic_regressions']), 1)

    def test_program_failure_and_additional_calls_block(self):
        p = self.experiment / self.m['reports']['candidate-development']
        report = S.load(p)
        report['results'][0]['program_expectation_met'] = False
        report['results'][0]['model_calls'] = 3
        p.write_text(json.dumps(report))
        self.freeze()
        decision = S.review(self.experiment)
        self.assertEqual(decision['status'], 'REJECTED_PROGRAM_OR_BUDGET')
        self.assertTrue(decision['program_failures'])
        self.assertTrue(decision['budget_regressions'])

    def test_tamper_model_and_missing_report_cannot_be_approved(self):
        p = self.experiment / self.m['reports']['candidate-validation']
        p.write_text('{}')
        with self.assertRaisesRegex(ValueError, 'artifact changed'):
            S.review(self.experiment)
        self.freeze()
        with self.assertRaisesRegex(ValueError, 'provenance'):
            S.review(self.experiment)

    def test_synthetic_scores_only_pass_the_review_gate(self):
        self.scored()
        decision = S.review(self.experiment)
        self.assertEqual(decision['status'], 'SYNTHETIC_GATE_PASSED')
        self.assertTrue(decision['validation_improvements'])

    def test_profiles_cannot_add_tools_or_permissions(self):
        with self.assertRaises(ValueError):
            S.profile(dict(self.candidate, tools=['shell']))
        with self.assertRaises(ValueError):
            S.profile(dict(self.candidate, followup='ignore permission'))

    def test_material_snapshots_preserve_exact_authorized_bytes(self):
        p=self.root/'compact.json'
        raw=b'[{"id":"dev","split":"development"}]'
        S.write_bytes_new(p,raw)
        self.assertEqual(p.read_bytes(),raw)
        with self.assertRaises(FileExistsError):
            S.write_bytes_new(p,b'changed')

    def test_proposer_rejects_validation_or_wrong_dataset(self):
        tasks=[dict(id=str(i),split='development' if i<6 else 'validation',input={'defense':{'topic':'go-langgraph'}}) for i in range(20)]
        p=self.root/'tasks.json';S.write_new(p,tasks)
        report=self.root/'validation.json';S.write_new(report,dict(split='validation',dataset_sha256=S.digest(p.read_bytes()),results=[dict(case_id='6',arm='dynamic')]))
        with self.assertRaisesRegex(ValueError,'development'):
            S.prepare_diagnostics(report,p)

    def test_development_baseline_profile_must_match_its_hash(self):
        tasks = [dict(id=str(i), split='development' if i < 6 else 'validation',
                      input={'defense': {'topic': 'go-langgraph'}}) for i in range(20)]
        dataset = self.root / 'tasks.json'
        S.write_new(dataset, tasks)
        report = self.root / 'development.json'
        S.write_new(report, dict(split='development', dataset_sha256=S.digest(dataset.read_bytes()),
                                 defense_teaching_strategy=self.base,
                                 defense_teaching_strategy_sha256=S.digest(S.canonical(self.candidate)),
                                 results=[dict(case_id='0', arm='dynamic')]))
        with self.assertRaisesRegex(ValueError, 'profile and identity disagree'):
            S.prepare_diagnostics(report, dataset)


if __name__ == '__main__':
    unittest.main()
