import importlib.util
import json
import pathlib
import stat
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('preparation_real_builder', pathlib.Path(__file__).with_name('build-preparation-real-eval.py'))
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class RealPreparationMaterialTests(unittest.TestCase):
    def test_contact_scrubbing_preserves_technical_numbers(self):
        text = '测试者\n158-1234-5678 person@example.invalid https://github.com/example/project\n1000/5000/10000 岗位，58% 分配优化，2027 届'
        clean = builder.scrub(text, '测试者')
        for private in ('测试者', '158-1234-5678', 'person@example.invalid', 'github.com/example'):
            self.assertNotIn(private, clean)
        self.assertIn('1000/5000/10000', clean)
        self.assertIn('58%', clean)

    def test_real_material_scenarios_never_invent_interview_history(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            resume = root / 'resume.txt';resume.write_text('测试者\n158-1234-5678 person@example.invalid\nGo 项目、MySQL 与 SQL 幂等')
            knowledge = root / 'handbook.md'
            knowledge.write_text('\n'.join(h+'\n只作学习参考。\n' for h in (
                '### 3.1 Go：任务、同步与退出','### 3.4 网络：从找到对端到结果未知',
                '### 5.1 查询与索引','### 5.2 事务、版本与最终约束','### 6.2 性能：先核对测量，再定位原因')))
            manifest=[]
            for key in ('s01','s03','s06'):
                text='岗位职责\n熟悉 Golang、网络通讯；2027 届。'
                (root/(key+'.txt')).write_text(text)
                manifest.append({'id':key,'company':'控制公司','title':'控制岗位','url':'https://example.invalid/job/'+key,'text_file':key+'.txt','text_sha256':builder.digest(text)})
            (root/'manifest.json').write_text(json.dumps(manifest))
            tasks, provenance = builder.build(resume,root,knowledge,[])
            self.assertEqual(len(tasks),21)
            self.assertEqual(sum(c['split']=='development' for c in tasks),3)
            self.assertEqual(sum(c['split']=='validation' for c in tasks),18)
            self.assertEqual(provenance['actual_interview_reviews'],0)
            self.assertEqual(len({c['id'] for c in tasks}),21)
            for task in tasks:
                self.assertFalse(any(s['kind']=='HISTORY' for s in task['sources']+task['corpus']))
                self.assertNotIn('person@example.invalid', json.dumps(task))
                for source in task['sources']+task['corpus']:
                    self.assertEqual(source['hash'],builder.digest(source['text']))
                if task['previous_plan']:
                    self.assertIn('controlled setup',task['review_note'])
            self.assertFalse(next(c for c in tasks if c['id']=='real-val-no-project-material')['previous_plan'])
            self.assertIn('15 分钟',next(c for c in tasks if c['id']=='real-val-15-minutes')['input']['goal'])
            self.assertNotIn('三小时',next(c for c in tasks if c['id']=='real-val-15-minutes')['input']['goal'])

    def test_private_output_is_exclusive_and_owner_readable(self):
        with tempfile.TemporaryDirectory() as directory:
            path=pathlib.Path(directory)/'tasks.json'
            builder.write_private(path,{'authorized':'material'})
            self.assertEqual(stat.S_IMODE(path.stat().st_mode),0o600)
            with self.assertRaises(FileExistsError):builder.write_private(path,{})


if __name__=='__main__':unittest.main()
