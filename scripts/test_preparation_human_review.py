import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name('build-preparation-human-review.py')
spec = importlib.util.spec_from_file_location('human_review', SCRIPT)
review = importlib.util.module_from_spec(spec)
spec.loader.exec_module(review)


class HumanReviewTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.dataset = self.root / 'tasks.json'
        self.dataset.write_text(json.dumps([{
            'id': 'case', 'input': {'minutes': 15, 'goal': '原始目标'},
            'sources': [{'id': 'jd', 'kind': 'JD', 'title': '岗位', 'text': '要求 Go 并发'}],
            'missing': ['没有真实复盘'],
        }], ensure_ascii=False))
        self.result = {'case_id': 'case', 'arm': 'dynamic', 'state': 'COMPLETED',
                       'program_expectation_met': True, 'model_calls': 1,
                       'plan': {'summary': '原始计划', 'items': [{
                           'title': '原始事项', 'minutes': 15,
                           'references': [{'source_id': 'jd', 'quote': 'Go 并发'}]}]}}
        self.report = self.root / 'source.json'
        self.report.write_text(json.dumps({
            'agent_version': 'test', 'mode': 'controlled-test-only',
            'dataset_sha256': hashlib.sha256(self.dataset.read_bytes()).hexdigest(),
            'results': [self.result]}, ensure_ascii=False))
        self.selection = self.root / 'selection.json'
        self.selection.write_text(json.dumps([{'report': str(self.report),
            'dataset': str(self.dataset), 'label': '示例', 'exercise': True}]))
        self.output = self.root / 'review'

    def test_preserves_output_provenance_and_unreviewed_scores(self):
        self.assertEqual(review.build(self.selection, self.output), 1)
        self.assertEqual((self.output / '01/report.json').read_bytes(), self.report.read_bytes())
        scores = json.loads((self.output / '01/scores.json').read_text())
        self.assertEqual(scores['report_sha256'], hashlib.sha256(self.report.read_bytes()).hexdigest())
        self.assertEqual(scores['reviews'][0]['reviewer_kind'], 'UNREVIEWED')
        self.assertTrue(all(cell['score'] is None for cell in scores['reviews'][0]['scores'].values()))
        page = (self.output / '01/审阅.md').read_text()
        self.assertIn('原始目标', page)
        self.assertIn('要求 Go 并发', page)
        self.assertIn('原始计划', page)
        exercise = (self.output / '15分钟实际执行.md').read_text()
        self.assertIn('原始事项 | 15', exercise)
        self.assertNotIn('|\n\n|', exercise)
        self.assertNotIn('|\n\n|', (self.output / '开始审阅.md').read_text())
        self.assertEqual((self.output / '01/scores.json').stat().st_mode & 0o777, 0o600)

    def test_rejects_changed_material_before_writing(self):
        self.dataset.write_text('[]')
        with self.assertRaisesRegex(ValueError, 'hash'):
            review.build(self.selection, self.output)
        self.assertFalse(self.output.exists())

    def test_defense_review_includes_memory_original_answer_and_unscored_quality(self):
        tasks=json.loads(self.dataset.read_text())
        tasks[0]['input']['defense']={'topic':'go-langgraph','memory_mode':'confirmed'}
        tasks[0]['memory_candidates']=[{'content':'原始纠正'}]
        self.dataset.write_text(json.dumps(tasks,ensure_ascii=False))
        report=json.loads(self.report.read_text())
        report['dataset_sha256']=hashlib.sha256(self.dataset.read_bytes()).hexdigest()
        report['semantic_rubric']={'dimensions':['answer_feedback_correctness','memory_added_value','correction_compliance']}
        report['results'][0]['defense']={'question':'本轮问题','answer':'本轮原回答','memories':[], 'evidence':[], 'review':{'summary':'具体反馈'}}
        self.report.write_text(json.dumps(report,ensure_ascii=False))
        review.build(self.selection,self.output)
        page=(self.output/'01/审阅.md').read_text()
        self.assertIn('本轮原回答',page)
        self.assertIn('原始纠正',page)
        self.assertIn('具体反馈',page)
        scores=json.loads((self.output/'01/scores.json').read_text())['reviews'][0]['scores']
        self.assertEqual(set(scores),set(report['semantic_rubric']['dimensions']))
        self.assertTrue(all(x['score'] is None for x in scores.values()))

    def test_never_overwrites_annotations(self):
        review.build(self.selection, self.output)
        notes = self.output / '01/scores.json'
        notes.write_text('用户已经填写的评价')
        with self.assertRaises(FileExistsError):
            review.build(self.selection, self.output)
        self.assertEqual(notes.read_text(), '用户已经填写的评价')

    def test_rejects_private_output_inside_repository(self):
        with self.assertRaisesRegex(ValueError, 'outside'):
            review.build(self.selection, review.ROOT / 'private-review-must-not-exist')


if __name__ == '__main__':
    unittest.main()
