import unittest
from unittest.mock import patch
from lab import Lab


class RecoveryTests(unittest.TestCase):
    def test_recovery_does_not_accept_an_empty_fulltext_index(self):
        lab=Lab.__new__(Lab);lab.users=[{'id':'alice','topics':['known']}];lab.company='Synthetic'
        lab.query=lambda user,topic:{'mode':'fulltext','jobs':[]}
        self.assertFalse(lab.fulltext_ready())
        lab.query=lambda user,topic:{'mode':'fulltext','jobs':[{'owner_id':'alice','company':'Synthetic'}]}
        self.assertTrue(lab.fulltext_ready())
        lab.query=lambda user,topic:{'mode':'fulltext','jobs':[{'owner_id':'bob','company':'Synthetic'}]}
        with self.assertRaises(ValueError):lab.fulltext_ready()

    def test_restart_refreshes_ephemeral_control_port_and_rejects_public_binding(self):
        lab=Lab.__new__(Lab);lab.compose=['docker','compose'];lab.urls={'worker':'http://127.0.0.1:10001'}
        with patch('lab.subprocess.check_output',return_value='127.0.0.1:10002\n'):
            lab.refresh_address('worker',18081)
        self.assertEqual(lab.urls['worker'],'http://127.0.0.1:10002')
        with patch('lab.subprocess.check_output',return_value='0.0.0.0:18081\n'):
            with self.assertRaises(ValueError):lab.refresh_address('worker',18081)


if __name__=='__main__':unittest.main()
