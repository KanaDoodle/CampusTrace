"""Application preparation graph. No credentials or SQL business rules in state."""
from dataclasses import dataclass
from typing import Any, TypedDict

import httpx
from langgraph.graph import END, START, StateGraph
from langgraph.runtime import Runtime
from langgraph.types import interrupt

GRAPH_VERSION = 'preparation-langgraph-v1'

class State(TypedDict, total=False):
    execution_id: str
    parent_id: str
    view: dict[str, Any]
    last_node: str
    continuation: str

@dataclass
class Bridge:
    client: httpx.AsyncClient
    capability: str  # runtime context; never serialized by the checkpointer

async def operation(state: State, runtime: Runtime[Bridge], action: str, node: str):
    response = await runtime.context.client.post(
        '/internal/preparation/step',
        headers={'Authorization': 'Bearer ' + runtime.context.capability},
        json={'action': action, 'sequence': state.get('view', {}).get('sequence', 0)},
    )
    response.raise_for_status()
    return {'view': response.json(), 'last_node': node}

def build_graph(checkpointer):
    builder = StateGraph(State, context_schema=Bridge)

    async def hydrate(state, runtime: Runtime[Bridge]):
        return await operation(state, runtime, 'sync', 'materials')

    async def model(state, runtime: Runtime[Bridge]):
        return await operation(state, runtime, 'model', 'model')

    async def retrieve(state, runtime: Runtime[Bridge]):
        return await operation(state, runtime, 'tool', 'retrieve')

    async def validate(state, runtime: Runtime[Bridge]):
        return await operation(state, runtime, 'tool', 'validate')

    async def review_answer(state, runtime: Runtime[Bridge]):
        return await operation(state, runtime, 'tool', 'review_answer')

    async def question(state, runtime: Runtime[Bridge]):
        return await operation(state, runtime, 'tool', 'question')

    def wait_input(state):
        # This node is deliberately side-effect-free before interrupt: resume
        # re-enters it. A reviewed child revision continues with fresh Go scope.
        value = interrupt({'kind': 'clarification', 'question': state['view']['preparation']['question']})
        return {'continuation': value['execution_id'], 'last_node': 'continued'}

    def wait_recovery(state):
        value = interrupt({'kind': 'recovery', 'provider_state': state['view']['preparation']['provider_state']})
        return {'view': value['view'], 'last_node': 'resumed'}

    def feedback(state):
        # Inputs are already re-reviewed by Go. This node records the parent
        # revision explicitly rather than treating feedback as new source facts.
        return {'last_node': 'feedback'}

    def route(state):
        view = state['view']
        prep = view['preparation']
        if prep.get('question'):
            return 'wait_input'
        if view['phase'] == 'PAUSED':
            return 'wait_recovery'
        if view['phase'] != 'RUNNING':
            return END
        pending = view.get('pending')
        if pending == 'ask_preparation_question':
            return 'question'
        if pending == 'finish_preparation_plan':
            return 'validate'
        if pending == 'finish_project_defense_review':
            return 'review_answer'
        if pending:
            return 'retrieve'
        return 'model'

    for name, node in [('materials', hydrate), ('feedback', feedback), ('model', model),
                       ('retrieve', retrieve), ('validate', validate), ('review_answer', review_answer), ('question', question),
                       ('wait_input', wait_input), ('wait_recovery', wait_recovery)]:
        builder.add_node(name, node)
    builder.add_conditional_edges(START, lambda s: 'feedback' if s.get('parent_id') else 'materials')
    builder.add_edge('feedback', 'materials')
    for node in ['materials', 'model', 'retrieve', 'validate', 'review_answer', 'question', 'wait_recovery']:
        builder.add_conditional_edges(node, route)
    builder.add_edge('wait_input', END)
    return builder.compile(checkpointer=checkpointer, name=GRAPH_VERSION)
