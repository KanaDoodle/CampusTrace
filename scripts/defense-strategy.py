#!/usr/bin/env python3
"""Offline teaching-policy experiments and attributable review.

No provider client or credential loading. Local evaluation uses controlled
responses; explicitly prepared real reports may be imported without new calls.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
PROFILE_FIELDS = {'schema', 'explain', 'followup', 'reading'}
LABELS = ('baseline', 'candidate')
SPLITS = ('development', 'validation')


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()


def digest(value):
    return hashlib.sha256(value).hexdigest()


def load(path):
    if path.stat().st_size > 8 * 1024 * 1024:
        raise ValueError('artifact too large')
    return json.loads(path.read_bytes())


def write_bytes_new(path, raw):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'wb') as f:
        f.write(raw)
        f.flush()
        os.fsync(f.fileno())


def write_new(path, value):
    write_bytes_new(path, json.dumps(value, ensure_ascii=False, indent=2).encode() + b'\n')


def profile(value):
    if set(value) != PROFILE_FIELDS or value['schema'] != 'defense-teaching-v1' or value['explain'] not in ('code-first', 'observation-first') or value['followup'] not in ('current-gap', 'contrast') or value['reading'] not in ('gap-only', 'read-one'):
        raise ValueError('teaching profile is outside the allowed choices')
    return value


def private(path):
    path = path.resolve()
    if path.is_relative_to(ROOT):
        raise ValueError('experiments and reviews must stay outside the repository')
    return path


def inside(root, name):
    p = (root / name).resolve()
    if not p.is_relative_to(root.resolve()):
        raise ValueError('artifact path escapes its root')
    return p


def code_sha():
    paths = []
    for folder in ('internal/agent', 'internal/domain', 'internal/defensestrategy', 'cmd/preparation-eval', 'cmd/defense-strategy'):
        paths += list((ROOT / folder).rglob('*.go'))
    paths += [ROOT / 'services/preparation/graph.py', ROOT / 'services/preparation/requirements.lock', ROOT / 'internal/projectdefense/catalog.json']
    paths += [ROOT / 'scripts/defense-strategy.py', ROOT / 'scripts/review-preparation-evaluation.py', ROOT / 'scripts/evaluate-preparation-langgraph.py']
    return digest(b''.join(str(p.relative_to(ROOT)).encode() + b'\0' + p.read_bytes() for p in sorted(paths)))


def environment():
    return dict(os.environ, GOWORK='off', LLM_API_KEY='', LLM_URL='', LLM_MODEL='', LANGSMITH_TRACING='false', LANGCHAIN_TRACING_V2='false')


def command(args, log):
    with log.open('x') as f:
        log.chmod(0o600)
        subprocess.run(args, cwd=ROOT, env=environment(), stdout=f, stderr=subprocess.STDOUT, check=True)


def prepare_diagnostics(report_path, dataset_path):
    data = dataset_path.read_bytes()
    if len(data)>1<<20:
        raise ValueError('dataset exceeds evaluation input limit')
    tasks = load(dataset_path)
    if len(tasks) < 20 or len(tasks) > 30 or len({t['id'] for t in tasks}) != len(tasks):
        raise ValueError('dataset requires 20-30 unique tasks')
    if {t['split'] for t in tasks} != set(SPLITS):
        raise ValueError('both dataset partitions are required')
    if any(not t.get('input', {}).get('defense') for t in tasks):
        raise ValueError('teaching optimization requires defense-only tasks')
    report = load(report_path)
    allowed = {t['id'] for t in tasks if t['split'] == 'development'}
    task_index = {t['id']: t for t in tasks}
    if report['split'] != 'development' or report['dataset_sha256'] != digest(data) or not report['results']:
        raise ValueError('proposer accepts only a matching development report')
    base = profile(report['defense_teaching_strategy'])
    if digest(canonical(base)) != report['defense_teaching_strategy_sha256']:
        raise ValueError('development report profile and identity disagree')
    seen = set()
    observations = []
    for r in report['results']:
        if r['case_id'] not in allowed or r['case_id'] in seen or r['arm'] != 'dynamic':
            raise ValueError('validation or duplicate results in proposal input')
        seen.add(r['case_id'])
        defense = r.get('defense') or {}
        feedback = defense.get('review') or {}
        observations.append(dict(case_id=r['case_id'], state=r['state'],
                                 program_expectation_met=r['program_expectation_met'], invalid_calls=r['invalid_calls'],
                                 trace=r.get('trace', [])[-8:], question=defense.get('question', ''),
                                 answer=defense.get('answer', ''), findings=feedback.get('findings', []),
                                 next_question=feedback.get('next_question', ''), minutes=task_index[r['case_id']]['input'].get('minutes', 0)))
    return dict(dataset_sha256=digest(data), source_report_sha256=digest(report_path.read_bytes()),
                split='development', base=base, observations=observations)


def run_experiment(args):
    out = private(args.output)
    out.mkdir(parents=True, mode=0o700, exist_ok=False)
    frame = code_sha()
    diagnostics = prepare_diagnostics(args.development_report, args.dataset)
    base = diagnostics['base']
    base_id = digest(canonical(base))
    write_new(out / 'diagnostics.json', diagnostics)
    write_bytes_new(out / 'dataset.json', args.dataset.read_bytes())
    write_bytes_new(out / 'source-development-report.json', args.development_report.read_bytes())
    write_new(out / 'baseline-profile.json', base)
    write_new(out / 'proposal-response.json', load(args.response))
    command(['go', 'run', './cmd/defense-strategy', '-input', str(out / 'diagnostics.json'), '-response', str(out / 'proposal-response.json'), '-output', str(out / 'proposal.json')], out / 'proposal.log')
    proposed = load(out / 'proposal.json')
    candidate = profile(proposed['candidate']['profile'])
    if proposed['candidate']['parent_sha256'] != base_id:
        raise ValueError('proposal parent differs from the development baseline')
    write_new(out / 'candidate-profile.json', candidate)
    artifacts = {p.name: digest(p.read_bytes()) for p in out.glob('*.json')}
    manifest = dict(schema='defense-strategy-experiment-v1', dataset_sha256=diagnostics['dataset_sha256'],
                    base_strategy_sha256=base_id, candidate_strategy_sha256=digest(canonical(candidate)),
                    code_sha256=frame, artifacts=artifacts, reports={}, mode='controlled-response-program-regression',
                    proposer_evidence=proposed['evidence'], external_model_calls=0)
    for split in SPLITS:
        for label in LABELS:
            destination = out / 'evaluations' / (label + '-' + split)
            if args.evaluation_root:
                src = args.evaluation_root / (label + '-' + split) / (split + '.json')
                write_new(destination / (split + '.json'), load(src))
            else:
                command([sys.executable, str(ROOT / 'scripts/evaluate-preparation-langgraph.py'), '--dataset', str(out / 'dataset.json'), '--arms', 'dynamic', '--splits', split, '--defense-strategy', str(out / (label + '-profile.json')), '--output', str(destination)], out / (label + '-' + split + '.log'))
            report_path = destination / (split + '.json')
            name = str(report_path.relative_to(out))
            manifest['artifacts'][name] = digest(report_path.read_bytes())
            manifest['reports'][label + '-' + split] = name
            command([sys.executable, str(ROOT / 'scripts/review-preparation-evaluation.py'), '--report', str(report_path), '--output', str(out / (label + '-' + split + '-scores.json'))], out / (label + '-' + split + '-review.log'))
        if split == 'development':
            check = program_gate(out, manifest, partitions=('development',))
            if check['program_failures'] or check['budget_regressions']:
                manifest['validation_skipped'] = 'candidate rejected on development; validation not exposed to proposer'
                break
    if frame != code_sha():
        raise ValueError('execution code changed during experiment')
    modes = {load(inside(out, n))['mode'] for n in manifest['reports'].values()}
    if len(modes) != 1:
        raise ValueError('mixed controlled and real evidence')
    manifest['mode'] = modes.pop()
    # Reuse the existing human-review formatting rather than a second grader.
    spec = importlib.util.spec_from_file_location('human_review', ROOT / 'scripts/build-preparation-human-review.py')
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    tasks = {t['id']: t for t in load(out / 'dataset.json')}
    lines = ['# 教学策略逐题对照\n', '以下是实际报告的阅读副本，原报告哈希由experiment.json绑定。只在对应 *-scores.json 填分和证据，不改本页。\n',
             '程序通过与语义质量分开；受控响应及SYNTHETIC_CONTROL评分仅验收机制。先独立核对当前回答和代码，再判断策略有无帮助。\n']
    for split in SPLITS:
        if 'baseline-' + split not in manifest['reports']:
            continue
        reports = {label: load(inside(out, manifest['reports'][label + '-' + split])) for label in LABELS}
        candidate_rows = {r['case_id']: r for r in reports['candidate']['results']}
        for base_result in reports['baseline']['results']:
            case = base_result['case_id']
            lines += ['## ' + split + ' · ' + case + '\n', '### 相同任务与约束\n', helper.block(tasks[case]['input'])]
            for label, result in [('baseline', base_result), ('candidate', candidate_rows[case])]:
                defense = result.get('defense') or {}
                lines += ['### ' + label + '\n', '填写 `' + label + '-' + split + '-scores.json` 中 `' + case + '` 一行。\n',
                          helper.block({'program_expectation_met': result['program_expectation_met'], 'state': result['state'],
                                        'model_calls': result['model_calls'], 'strategy': defense.get('teaching_strategy'),
                                        'prompt_sha256': result.get('prompt_sha256'), 'question': defense.get('question'), 'answer': defense.get('answer'),
                                        'memories': defense.get('memories', []), 'review': defense.get('review'), 'trace': result.get('trace', [])})]
                for source in defense.get('evidence', []):
                    lines += ['#### 代码依据 · ' + source['title'] + '\n', helper.block(source['text'])]
    page = out / '逐题对照.md'
    page.write_text(helper.markdown(lines))
    page.chmod(0o600)
    manifest['artifacts'][page.name] = digest(page.read_bytes())
    write_new(out / 'experiment.json', manifest)
    decision = review(out)
    write_new(out / 'initial-decision.json', decision)
    (out / '审阅说明.md').write_text('''# 教学策略对照审阅

先看 diagnostics.json、proposal.json 中的假设及调试集来源，再对照 evaluations 中的原回答、记忆、反馈、引用与轨迹。
逐份填写 *-scores.json 的 reviewer、reviewer_kind、score 与具体证据；支持 TARGET_USER_SELF_REVIEW 或 INDEPENDENT_HUMAN。
受控响应不能作为真实模型提升，测试用合成标注必须用 SYNTHETIC_CONTROL，不能伪装成人工审阅。
调试和验证分别比较，每个语义维度保留退化；验证集至少一处改善且无退化才可通过。空分数保持待审阅。
不依据模型自评采用策略。使用已有 build-preparation-human-review.py 可进一步导出逐题材料。
运行 scripts/defense-strategy.py review --experiment <本目录> --output <新的决策文件>，对照结果可用于代码评审。
该命令只读本地材料，不调用模型。实验快照不可编辑；评分文件可填写，旧决策不可覆盖。
''')
    (out / '审阅说明.md').chmod(0o600)
    return decision


def verify_artifacts(out, m):
    if m['schema'] != 'defense-strategy-experiment-v1' or m['code_sha256'] != code_sha():
        raise ValueError('experiment code no longer matches')
    for name, expected in m['artifacts'].items():
        if digest(inside(out, name).read_bytes()) != expected:
            raise ValueError('experiment artifact changed: ' + name)
    if digest(canonical(profile(load(out / 'baseline-profile.json')))) != m['base_strategy_sha256'] or digest(canonical(profile(load(out / 'candidate-profile.json')))) != m['candidate_strategy_sha256']:
        raise ValueError('profile identity mismatch')


def program_gate(out, m, partitions=SPLITS):
    tasks = load(out / 'dataset.json')
    failures, budgets = [], []
    fingerprints = []
    for split in partitions:
        by_label = {}
        expected = {t['id'] for t in tasks if t['split'] == split}
        for label in LABELS:
            r = load(inside(out, m['reports'][label + '-' + split]))
            required = {'mode', 'model_config', 'max_calls', 'per_task_model_limit', 'framework_versions', 'graph_version', 'agent_version', 'tools_version', 'project_evidence_sha256', 'quality_version', 'defense_prompt_version', 'defense_teaching_strategy', 'defense_teaching_strategy_sha256', 'dynamic_executor', 'external_model_attempts'}
            if not required.issubset(r) or r['mode'] not in ('controlled-response-program-regression', 'real-model-on-explicitly-authorized-materials') or r['dynamic_executor'] != 'langgraph':
                raise ValueError('incomplete execution provenance')
            if r['split'] != split or r['dataset_sha256'] != m['dataset_sha256'] or r['arms'] != ['dynamic'] or r['defense_teaching_strategy_sha256'] != m[label.replace('baseline', 'base') + '_strategy_sha256']:
                raise ValueError('report scope or strategy differs')
            if digest(canonical(profile(r['defense_teaching_strategy']))) != r['defense_teaching_strategy_sha256']:
                raise ValueError('report profile and identity disagree')
            fingerprints.append(canonical({k: r[k] for k in ('mode', 'model_config', 'max_calls', 'per_task_model_limit', 'framework_versions', 'graph_version', 'agent_version', 'tools_version', 'project_evidence_sha256', 'quality_version', 'defense_prompt_version')}))
            rows = {v['case_id']: v for v in r['results']}
            if set(rows) != expected or len(rows) != len(r['results']) or any(v['arm'] != 'dynamic' for v in rows.values()):
                raise ValueError('evaluation is missing or duplicating cases')
            by_label[label] = rows
        for case, candidate in by_label['candidate'].items():
            baseline = by_label['baseline'][case]
            if not candidate['program_expectation_met']:
                failures.append(dict(split=split, case_id=case, state=candidate['state'], baseline_met=baseline['program_expectation_met']))
            if candidate['model_calls'] > baseline['model_calls']:
                budgets.append(dict(split=split, case_id=case, baseline_calls=baseline['model_calls'], candidate_calls=candidate['model_calls']))
    if len(set(fingerprints)) != 1:
        raise ValueError('model, material, execution or budget configuration differs')
    return dict(program_failures=failures, budget_regressions=budgets)


def review(out):
    out = private(out)
    manifest_path = out / 'experiment.json'
    m = load(manifest_path)
    verify_artifacts(out, m)
    gate = program_gate(out, m, partitions=('development',) if m.get('validation_skipped') else SPLITS)
    decision = dict(schema='defense-strategy-decision-v1', experiment_sha256=digest(manifest_path.read_bytes()),
                    **gate, mode=m['mode'], semantic_regressions=[], validation_improvements=[], review_sha256={}, status='PENDING_HUMAN_REVIEW')
    if gate['program_failures'] or gate['budget_regressions']:
        decision['status'] = 'REJECTED_PROGRAM_OR_BUDGET'
        return decision
    if m['base_strategy_sha256'] == m['candidate_strategy_sha256']:
        decision['status'] = 'NO_CHANGE'
        return decision
    pending = False
    for split in SPLITS:
        reviews = {}
        for label in LABELS:
            report_path = inside(out, m['reports'][label + '-' + split])
            path = out / (label + '-' + split + '-scores.json')
            scored = load(path)
            report = load(report_path)
            if scored['report_sha256'] != digest(report_path.read_bytes()):
                raise ValueError('review refers to another report')
            expected = {r['case_id'] for r in report['results']}
            rows = {r['case_id']: r for r in scored['reviews']}
            if set(rows) != expected or len(rows) != len(scored['reviews']):
                raise ValueError('incomplete or duplicate review')
            dimensions = report['semantic_rubric']['dimensions']
            if set(dimensions) != {'answer_feedback_correctness', 'citation_entailment', 'feasible_workload', 'feedback_compliance', 'no_fabricated_experience', 'memory_added_value', 'correction_compliance'}:
                raise ValueError('semantic rubric differs')
            for row in rows.values():
                if row['arm'] != 'dynamic' or set(row['scores']) != set(dimensions):
                    raise ValueError('invalid review scope')
                for cell in row['scores'].values():
                    value = cell['score']
                    if value is None:
                        pending = True
                        continue
                    if (type(value) is not int or value not in (0, 1, 2)) and value != 'N/A':
                        raise ValueError('invalid score')
                    if not row['reviewer'].strip() or row['reviewer_kind'] not in ('TARGET_USER_SELF_REVIEW', 'INDEPENDENT_HUMAN', 'SYNTHETIC_CONTROL') or not cell['evidence'].strip():
                        raise ValueError('scores need attributable reviewer and evidence')
                    if row['reviewer_kind'] == 'SYNTHETIC_CONTROL' and m['mode'] != 'controlled-response-program-regression':
                        raise ValueError('synthetic labels cannot approve real evidence')
            decision['review_sha256'][path.name] = digest(path.read_bytes())
            reviews[label] = rows
        for case, row in reviews['candidate'].items():
            for dimension, cell in row['scores'].items():
                a, b = reviews['baseline'][case]['scores'][dimension]['score'], cell['score']
                if a is None or b is None:
                    continue
                if a == b == 'N/A':
                    continue
                if a == 'N/A' or b == 'N/A':
                    raise ValueError('N/A comparability needs reviewer resolution')
                if b < a:
                    decision['semantic_regressions'].append(dict(split=split, case_id=case, dimension=dimension, baseline=a, candidate=b))
                elif b > a and split == 'validation':
                    decision['validation_improvements'].append(dict(case_id=case, dimension=dimension, baseline=a, candidate=b))
    if decision['semantic_regressions']:
        decision['status'] = 'REJECTED_SEMANTIC_REGRESSION'
    elif pending:
        decision['status'] = 'PENDING_HUMAN_REVIEW'
    elif decision['validation_improvements']:
        decision['status'] = 'SYNTHETIC_GATE_PASSED' if m['mode'] == 'controlled-response-program-regression' else 'REVIEWED_IMPROVEMENT'
    else:
        decision['status'] = 'NO_GAIN'
    return decision


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    p = sub.add_parser('run')
    p.add_argument('--dataset', type=Path, required=True)
    p.add_argument('--development-report', type=Path, required=True)
    p.add_argument('--response', type=Path, required=True)
    p.add_argument('--output', type=Path, required=True)
    p.add_argument('--evaluation-root', type=Path, help='import authorized existing reports; never requests a provider')
    p = sub.add_parser('review')
    p.add_argument('--experiment', type=Path, required=True)
    p.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if args.action == 'run':
        result = run_experiment(args)
    elif args.action == 'review':
        result = review(args.experiment)
        write_new(args.output, result)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as e:
        print(str(e), file=sys.stderr)
        sys.exit(1)
