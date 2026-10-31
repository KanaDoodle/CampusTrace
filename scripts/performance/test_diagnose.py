from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from diagnose import Diagnosis, frozen

class IsolationTests(unittest.TestCase):
    def test_transient_unassigned_port_is_retried_before_connecting(self):
        instance=Diagnosis.__new__(Diagnosis)
        instance.compose=['docker','compose'];instance.urls={}
        with patch('lab.subprocess.check_output',side_effect=['invalid IP:0','127.0.0.1:18000']), patch('diagnose.time.sleep'):
            instance.refresh_address('search',18083)
        self.assertEqual(instance.urls['search'],'http://127.0.0.1:18000')

    def test_sql_refuses_an_existing_business_schema(self):
        instance=Diagnosis.__new__(Diagnosis)
        instance.id='abcdef123456'
        instance.environment={'CAMPUS_DATABASE':'campustrace'}
        with patch('diagnose.subprocess.run') as execute:
            with self.assertRaises(ValueError): instance.sql('SELECT 1')
            execute.assert_not_called()

    def test_sparse_fixture_uses_exact_ingest_receipts(self):
        instance=Diagnosis.__new__(Diagnosis);instance.sparse=[None,None]
        instance.seeded_job(996,{'job_id':'bob-job'})
        instance.seeded_job(998,{'job_id':'alice-job'})
        self.assertEqual(instance.sparse,[{'text':'synthetic00998','id':'alice-job'},
                                          {'text':'synthetic00996','id':'bob-job'}])

    def test_freeze_requires_search_runtime(self):
        with tempfile.TemporaryDirectory() as root:
            source=Path(root)/'source'; source.mkdir()
            with self.assertRaises(ValueError): frozen(source,Path(root)/'target')

    def test_stopped_owned_database_fails_before_readiness_retry(self):
        instance=Diagnosis.__new__(Diagnosis);instance.project='ct-perf-abcdef123456'
        with tempfile.TemporaryDirectory() as root:
            instance.evidence=Path(root)
            with patch('diagnose.subprocess.check_output',return_value='{"Running":false,"OOMKilled":true}'):
                with self.assertRaisesRegex(RuntimeError,'OOM=True'):
                    instance.wait('ready',lambda:False,1)
            self.assertTrue((Path(root)/'mysql-failed-state.json').is_file())

if __name__=='__main__': unittest.main()
