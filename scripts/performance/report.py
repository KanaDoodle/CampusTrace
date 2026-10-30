"""Fail closed on incomplete or incomparable performance evidence."""
import argparse
import json
import math
from pathlib import Path

PHASES = ('fulltext', 'fallback', 'rebuild', 'mixed')


def metric(phase, name, key, default=None):
    value = phase.get('metrics', {}).get(name, {}).get('values', {}).get(key, default)
    if value is not None and (not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0):
        raise ValueError('Invalid finite metric: ' + name)
    return value


def validate(report):
    errors = []
    if report.get('schema_version') != 1:
        errors.append('Unsupported evidence schema')
    if report.get('status') == 'failed':
        errors.append('The recorded run failed')
    workload = report.get('workload', {})
    phases = report.get('phases', {})
    for name in PHASES:
        phase = phases.get(name)
        if not phase:
            errors.append(name + ': missing phase')
            continue
        queries = metric(phase, 'query_requests', 'count', 0)
        for required in ('query_requests','query_failures','http_req_failed','query_ms'):
            if required not in phase.get('metrics', {}):
                errors.append(name + ': missing metric ' + required)
        minimum = math.floor(workload['query_rate'] * workload['seconds'] * .9)
        if queries < minimum:
            errors.append(name + ': insufficient completed query attempts')
        for m, key in [('query_failures', 'count'), ('http_req_failed', 'rate'), ('dropped_iterations', 'count')]:
            if metric(phase, m, key, 0) != 0:
                errors.append(name + ': ' + m)
        if metric(phase, 'checks', 'rate') != 1:
            errors.append(name + ': failed or missing response checks')
        if metric(phase, 'query_ms', 'p(95)') is None or metric(phase, 'query_ms', 'p(99)') is None:
            errors.append(name + ': missing latency quantiles')
        if name == 'fulltext' and (metric(phase, 'mode_fulltext', 'count', 0) != queries or metric(phase, 'mode_basic', 'count', 0) != 0):
            errors.append(name + ': unexpected fallback')
        if name == 'fallback' and (metric(phase, 'mode_basic', 'count', 0) != queries or metric(phase, 'mode_fulltext', 'count', 0) != 0):
            errors.append(name + ': wrong query mode')
        if name == 'rebuild' and (metric(phase, 'mode_basic', 'count', 0) < 1 or not phase.get('rebuild_injected')):
            errors.append(name + ': rebuild fallback was not observed')
        if name == 'mixed':
            if metric(phase, 'write_requests', 'count', 0) < math.floor(workload['write_rate'] * workload['seconds'] * .9):
                errors.append(name + ': missing concurrent writes')
            if metric(phase, 'write_failures', 'count', 0) != 0 or not phase.get('latest_write_visible'):
                errors.append(name + ': update verification failed')
        writes = metric(phase, 'write_requests', 'count', 0)
        if metric(phase, 'iterations', 'count') != queries + writes or metric(phase, 'http_reqs', 'count') != queries + writes:
            errors.append(name + ': incomplete load iterations or HTTP accounting')
        if phase.get('exit_code') != 0:
            errors.append(name + ': load generator failed')
        if not phase.get('recovered_fulltext'):
            errors.append(name + ': fulltext did not recover')
    return errors


def compare(candidate, baseline, ratio=1.5, allowance_ms=25):
    if not math.isfinite(ratio) or not math.isfinite(allowance_ms) or ratio<1 or allowance_ms<0:
        raise ValueError('Invalid regression policy')
    errors = validate(candidate) + ['baseline: ' + error for error in validate(baseline)]
    if candidate.get('schema_version') != 1 or baseline.get('schema_version') != 1:
        errors.append('Unsupported evidence schema')
    if candidate.get('workload') != baseline.get('workload'):
        errors.append('Workload differs; quantiles are not comparable')
    if candidate.get('environment') != baseline.get('environment'):
        errors.append('Environment differs; quantiles are not comparable')
    comparisons = []
    if errors:
        return errors, comparisons
    # Transient phase mode proportions differ, so only steady phases gate latency.
    for name in ('fulltext', 'fallback'):
        count = metric(candidate['phases'][name], 'query_requests', 'count', 0)
        base_count = metric(baseline['phases'][name], 'query_requests', 'count', 0)
        if min(count, base_count) < 100:
            errors.append(name + ': need at least 100 samples for regression comparison')
            continue
        for quantile in ('p(95)', 'p(99)'):
            old = metric(baseline['phases'][name], 'query_ms', quantile)
            new = metric(candidate['phases'][name], 'query_ms', quantile)
            limit = old * ratio + allowance_ms
            passed = new <= limit
            comparisons.append({'phase':name,'quantile':quantile,'baseline_ms':old,'candidate_ms':new,'limit_ms':limit,'passed':passed})
            if not passed:
                errors.append(name + ': ' + quantile + ' regression')
    return errors, comparisons


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('candidate', type=Path)
    parser.add_argument('--baseline', type=Path)
    parser.add_argument('--ratio', type=float, default=1.5)
    parser.add_argument('--allowance-ms', type=float, default=25)
    args = parser.parse_args()
    if not math.isfinite(args.ratio) or not math.isfinite(args.allowance_ms) or args.ratio < 1 or args.allowance_ms < 0:
        parser.error('Invalid regression policy')
    candidate = json.loads(args.candidate.read_text())
    if args.baseline:
        errors, comparisons = compare(candidate, json.loads(args.baseline.read_text()), args.ratio, args.allowance_ms)
    else:
        errors, comparisons = validate(candidate), []
    print(json.dumps({'passed':not errors,'errors':errors,'comparisons':comparisons}, indent=2))
    return 1 if errors else 0


if __name__ == '__main__':
    raise SystemExit(main())
