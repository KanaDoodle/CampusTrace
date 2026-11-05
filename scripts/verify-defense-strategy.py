#!/usr/bin/env python3
"""Exercise proposals, actual LangGraph comparisons and offline review.

Every model response and filled review below is explicitly synthetic control.
No provider client is invoked.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import shutil
import sys

spec = importlib.util.spec_from_file_location('strategy', Path(__file__).with_name('defense-strategy.py'))
S = importlib.util.module_from_spec(spec)
spec.loader.exec_module(S)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    out = S.private(args.output)
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    dataset = S.ROOT / 'internal/agent/testdata/project-defense-tasks.json'
    checks = []

    def experiment(name, changes=None, knobs=None):
        tasks = S.load(dataset)
        if changes:
            for split, control in changes.items():
                next(t for t in tasks if t['split'] == split)['control'] = control
        task_path = out / (name + '-tasks.json')
        S.write_bytes_new(task_path,json.dumps(tasks,ensure_ascii=False,separators=(',',':')).encode())
        seed = out / (name + '-seed')
        S.command([sys.executable, str(S.ROOT / 'scripts/evaluate-preparation-langgraph.py'), '--dataset', str(task_path), '--arms', 'dynamic', '--splits', 'development', '--output', str(seed)], out / (name + '-seed.log'))
        response = out / (name + '-response.json')
        seed_report=S.load(seed/'development.json')
        selected = dict(seed_report['defense_teaching_strategy'], followup='contrast', **(knobs or {}))
        linked=next((r['case_id'] for r in seed_report['results'] if r['invalid_calls'] or not r['program_expectation_met']),tasks[0]['id'])
        S.write_new(response, dict(parent_sha256=seed_report['defense_teaching_strategy_sha256'], hypothesis='根据调试集中已记录的参数纠错，尝试更明确的对照提问；此为受控提案，不宣称质量改善。', case_ids=[linked], profile=selected))
        target = out / name
        a = argparse.Namespace(output=target, dataset=task_path, development_report=seed / 'development.json', response=response, evaluation_root=None)
        decision = S.run_experiment(a)
        manifest = S.load(target / 'experiment.json')
        assert (target/'dataset.json').read_bytes()==task_path.read_bytes()
        assert (target/'source-development-report.json').read_bytes()==(seed/'development.json').read_bytes()
        diagnostics = S.load(target / 'diagnostics.json')
        assert {x['case_id'] for x in diagnostics['observations']} <= {t['id'] for t in tasks if t['split'] == 'development'}
        assert all('profile' not in r for r in diagnostics['observations'])
        assert all(r.get('answer') and r.get('findings') for r in diagnostics['observations'])
        return target, manifest, decision

    pending, manifest, decision = experiment('pending')
    assert decision['status'] == 'PENDING_HUMAN_REVIEW'
    assert (pending/'逐题对照.md').exists()
    b=S.load(pending/manifest['reports']['baseline-validation'])
    c=S.load(pending/manifest['reports']['candidate-validation'])
    assert all(x['prompt_sha256']!=y['prompt_sha256'] for x,y in zip(b['results'],c['results']))
    assert all(x['defense']['teaching_strategy']['followup']=='contrast' for x in c['results'])
    checks += ['controlled_proposer_consumes_only_development_trajectories', 'candidate_reaches_real_go_python_langgraph', 'empty_reviews_remain_pending']

    def annotated(name, a, b, degrade=False):
        target = out / name
        shutil.copytree(pending, target)
        for path in target.glob('*-scores.json'):
            sidecar = S.load(path)
            for row in sidecar['reviews']:
                row['reviewer'] = 'synthetic-gate-fixture'
                row['reviewer_kind'] = 'SYNTHETIC_CONTROL'
                for cell in row['scores'].values():
                    cell.update(score=a if path.name.startswith('baseline') else b,
                                evidence='仅供准入机制验收的合成分数，不是人工质量审阅。')
            if degrade and path.name == 'candidate-validation-scores.json':
                sidecar['reviews'][0]['scores']['citation_entailment']['score'] = 0
            path.write_text(json.dumps(sidecar, ensure_ascii=False, indent=2) + '\n')
        result = S.review(target)
        S.write_new(target / 'synthetic-decision.json', result)
        return target, result

    _, no_gain = annotated('no-gain', 1, 1)
    assert no_gain['status'] == 'NO_GAIN'
    _, degraded = annotated('semantic-regression', 1, 2, True)
    assert degraded['status'] == 'REJECTED_SEMANTIC_REGRESSION' and degraded['semantic_regressions']
    _, synthetic = annotated('synthetic-gate', 1, 2)
    assert synthetic['status'] == 'SYNTHETIC_GATE_PASSED' and synthetic['validation_improvements']
    checks += ['no_gain_retained', 'semantic_regression_blocks_aggregate_gain', 'synthetic_scores_report_gate_only']

    _, _, decision = experiment('development-regression', {'development': 'strategy-contract-probe'}, {'explain': 'observation-first'})
    assert decision['status'] == 'REJECTED_PROGRAM_OR_BUDGET' and decision['program_failures']
    assert S.load(out / 'development-regression/experiment.json').get('validation_skipped')
    checks.append('development_regression_skips_validation')
    _, _, decision = experiment('budget-regression', {'development': 'strategy-cost-probe'}, {'reading': 'read-one'})
    assert decision['status'] == 'REJECTED_PROGRAM_OR_BUDGET' and decision['budget_regressions'] and not decision['program_failures']
    checks.append('extra_model_call_rejected_even_when_plan_valid')
    _, _, decision = experiment('validation-regression', {'validation': 'strategy-validation-probe'})
    assert decision['status'] == 'REJECTED_PROGRAM_OR_BUDGET' and any(r['split'] == 'validation' for r in decision['program_failures'])
    checks.append('heldout_regression_retained_and_not_fed_to_proposer')
    result = dict(status='PASS', evidence='controlled-Go-Python-LangGraph-and-synthetic-review-fixtures', external_model_calls=0,
                  checks=checks, semantic_quality='UNREVIEWED; synthetic labels test gates only', baseline_strategy=manifest['base_strategy_sha256'])
    S.write_new(out / 'result.json', result)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
