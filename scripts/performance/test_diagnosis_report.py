import unittest
from diagnosis_report import summarize

class SummaryTests(unittest.TestCase):
    def test_failed_and_dropped_requests_remain_in_report(self):
        def metric(**values): return {'values': values}
        report={'phases':{'1-baseline-basic':{'exit_code':99,'metrics':{
            'miss_ms':metric(med=1000,**{'p(95)':1100,'p(99)':1200}),
            'miss_attempts':metric(count=10),'miss_failures':metric(count=4),
            'dropped_iterations':metric(count=3),
        }}}}
        miss=[r for r in summarize(report) if r['class']=='miss'][0]
        self.assertEqual((miss['attempts'],miss['failures'],miss['dropped'],miss['k6_exit']),(10,4,3,99))
        self.assertEqual(miss['p99_ms'],1200)

if __name__=='__main__': unittest.main()
