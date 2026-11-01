#!/usr/bin/env python3
"""Build/check a fixed allowlist of project excerpts. No model or private files."""
import argparse
import hashlib
import json
import pathlib
import subprocess

ROOT = pathlib.Path(__file__).resolve().parents[1]
OUTPUT = ROOT / 'internal/projectdefense/catalog.json'


def excerpt(identity, path, start, end, kind='CODE'):
    raw = (ROOT / path).read_bytes()
    text = raw.decode()
    begin = text.index(start)
    finish = text.index(end, begin + len(start))
    piece = text[begin:finish]
    if len(piece.encode()) > 20000:
        raise ValueError('Evidence exceeds the reviewed excerpt limit')
    return dict(id=identity, path=path, start_line=text[:begin].count('\n') + 1,
                end_line=text[:finish].count('\n') + 1, kind=kind, text=piece,
                sha256=hashlib.sha256(piece.encode()).hexdigest(),
                file_sha256=hashlib.sha256(raw).hexdigest())


def build():
    return {'base_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
            'snapshot_kind': 'reviewed-working-tree-excerpts; file hashes identify exact input; not historical application material',
            'topics': [
                {'id': 'go-langgraph', 'title': 'Go 与 LangGraph 怎样配合准备面试',
                 'question': '从核对申请材料到确认保存计划，Go 和 LangGraph 分别做什么？真正的模型请求由谁发出？',
                 'points': [{'id': 'business', 'text': 'Go核对材料与账号、执行受控操作，并校验业务保存', 'evidence_ids':['plan-save','go-bridge']},
                            {'id': 'routing', 'text': 'LangGraph根据操作结果衔接模型、工具、等待与结束', 'evidence_ids':['graph-routing']},
                            {'id': 'model', 'text': '模型请求由Go发送；模型选择动作，框架本身不生成答案', 'evidence_ids':['model-dispatch','go-bridge']}],
                 'evidence': [
                     excerpt('graph-routing', 'services/preparation/graph.py', '    async def hydrate', '    for name, node'),
                     excerpt('go-bridge', 'internal/agent/preparation_graph.go', 'func (g *PreparationGraphClient) InternalStep', '\n}', 'CODE'),
                     excerpt('plan-save', 'internal/persistence/preparation_agent.go', 'func savePreparationTx', '\nfunc preparationAgentIdentityQuery'),
                     excerpt('model-dispatch', 'internal/agent/preparation_agent_loop.go', '\t\tstate.ModelCalls++', '\t\tstate.ProviderState = "READY"'),
                 ]},
                {'id': 'unknown-recovery', 'title': '模型结果未知时，为什么不能直接重试',
                 'question': '模型请求已发出，但服务中断后没有保存响应。为什么不自动重试？已完成读取和用户授权的新尝试怎样处理？',
                 'points': [{'id': 'unknown', 'text': '没有保存响应不能证明请求未执行；保留UNKNOWN，避免自动重复付费调用', 'evidence_ids':['model-dispatch']},
                            {'id': 'reuse', 'text': '已完成步骤复用回执，未完成步骤与未知请求分别处理', 'evidence_ids':['step-receipt','recovery-test']},
                            {'id': 'consent', 'text': '新的未知请求尝试需要用户明确授权，并继续受权限和版本约束', 'evidence_ids':['unknown-ack']}],
                 'evidence': [
                     excerpt('model-dispatch', 'internal/agent/preparation_agent_loop.go', '\t\tstate.ModelCalls++', '\t\tstate.ProviderState = "READY"'),
                     excerpt('step-receipt', 'internal/agent/preparation_graph.go', '\t// Replayed nodes receive', '\tctx, cancel := context.WithCancel(s.ctx)'),
                     excerpt('unknown-ack', 'internal/persistence/agent_execution.go', '\t\tif claim != nil && !claim.RetryUnknown', '\n\t\tv.Resumes++'),
                     excerpt('recovery-test', 'internal/agent/preparation_agent_test.go', 'func TestPreparationUnknownModelAndDurablePendingTools', '\nfunc TestPreparationBoundsAndClarification', 'TEST'),
                 ]},
            ]}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--check', action='store_true')
    args = p.parse_args()
    catalog = build()
    if args.check:
        old = json.loads(OUTPUT.read_text())
        # Commits can change without these files changing. The snapshot's file
        # hashes and exact text, not the current HEAD label, are authoritative.
        catalog['base_commit'] = old['base_commit']
        if old != catalog:
            raise SystemExit('Project evidence is stale; regenerate and review changed excerpts')
        print('Project evidence paths, excerpts, line ranges and hashes match the working tree.')
    else:
        OUTPUT.write_text(json.dumps(catalog, ensure_ascii=False, indent=2) + '\n')
        print('Generated two-topic evidence bundle; review excerpts before publishing.')


if __name__ == '__main__':
    main()
