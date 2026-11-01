import json
import tempfile
import unittest
from pathlib import Path

import httpx
from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
from langgraph.types import Command
from graph import Bridge, build_graph

class GraphTests(unittest.IsolatedAsyncioTestCase):
    async def test_project_answer_uses_review_node(self):
        operations=[]
        sequence=0
        def handle(request):
            nonlocal sequence
            action=json.loads(request.content)['action'];operations.append(action)
            if action!='sync':sequence+=1
            pending,phase={0:('', 'RUNNING'),1:('read_project_defense_evidence','RUNNING'),
                           2:('', 'RUNNING'),3:('finish_project_defense_review','RUNNING'),4:('', 'COMPLETED')}[sequence]
            return httpx.Response(200,json={'sequence':sequence,'phase':phase,'pending':pending,'preparation':{'provider_state':'READY'}})
        with tempfile.TemporaryDirectory() as directory:
            async with AsyncSqliteSaver.from_conn_string(str(Path(directory)/'graph.sqlite')) as saver:
                graph=build_graph(saver)
                async with httpx.AsyncClient(base_url='http://go.test',transport=httpx.MockTransport(handle)) as client:
                    result=await graph.ainvoke({'execution_id':'e'*32},{'configurable':{'thread_id':'e'*32}},context=Bridge(client,'runtime-only-secret'))
                self.assertEqual(result['last_node'],'review_answer')
                self.assertEqual(operations,['sync','model','tool','model','tool'])

    async def test_routes_observation_to_rewrite_then_finish(self):
        operations=[]
        sequence=0
        def handle(request):
            nonlocal sequence
            body=json.loads(request.content);op=body['action'];operations.append(op)
            if op!='sync':sequence+=1
            # First search is empty; model decides to search again, then finish.
            nodes={0:('', 'RUNNING'),1:('search_preparation_knowledge','RUNNING'),
                   2:('', 'RUNNING'),3:('search_preparation_knowledge','RUNNING'),
                   4:('', 'RUNNING'),5:('finish_preparation_plan','RUNNING'),6:('', 'COMPLETED')}
            pending,phase=nodes[sequence]
            return httpx.Response(200,json={'sequence':sequence,'phase':phase,'pending':pending,
                'preparation':{'provider_state':'READY','question':''}})
        with tempfile.TemporaryDirectory() as directory:
            async with AsyncSqliteSaver.from_conn_string(str(Path(directory)/'graph.sqlite')) as saver:
                graph=build_graph(saver)
                async with httpx.AsyncClient(base_url='http://go.test',transport=httpx.MockTransport(handle)) as client:
                    result=await graph.ainvoke({'execution_id':'a'*32},{'configurable':{'thread_id':'a'*32}},context=Bridge(client,'secret-capability'),durability='sync')
                self.assertEqual(result['view']['phase'],'COMPLETED')
                self.assertEqual(operations,['sync','model','tool','model','tool','model','tool'])
                raw=Path(directory,'graph.sqlite').read_bytes()
                self.assertNotIn(b'secret-capability',raw)

    async def test_clarification_survives_checkpointer_close_and_reopen(self):
        with tempfile.TemporaryDirectory() as directory:
            path=str(Path(directory)/'graph.sqlite');cfg={'configurable':{'thread_id':'b'*32}}
            def handle(request):
                op=json.loads(request.content)['action']
                return httpx.Response(200,json={'sequence':1,'phase':'PAUSED','preparation':{
                    'provider_state':'READY','question':'还可准备多少分钟？'}})
            async with AsyncSqliteSaver.from_conn_string(path) as saver:
                graph=build_graph(saver)
                async with httpx.AsyncClient(base_url='http://go.test',transport=httpx.MockTransport(handle)) as client:
                    result=await graph.ainvoke({'execution_id':'b'*32},cfg,context=Bridge(client,'never-checkpoint-this'))
                self.assertTrue(result['__interrupt__'])
            async with AsyncSqliteSaver.from_conn_string(path) as saver:
                graph=build_graph(saver);snapshot=await graph.aget_state(cfg)
                self.assertEqual(snapshot.next,('wait_input',))
                result=await graph.ainvoke(Command(resume={'execution_id':'c'*32}),cfg)
                self.assertEqual(result['continuation'],'c'*32)
                self.assertFalse((await graph.aget_state(cfg)).next)
            self.assertNotIn(b'never-checkpoint-this',Path(path).read_bytes())

    async def test_unknown_interrupt_does_not_auto_dispatch(self):
        count=0
        def handle(request):
            nonlocal count
            count+=1
            return httpx.Response(200,json={'sequence':0,'phase':'PAUSED',
                'preparation':{'provider_state':'UNKNOWN'}})
        with tempfile.TemporaryDirectory() as directory:
            async with AsyncSqliteSaver.from_conn_string(str(Path(directory)/'graph.sqlite')) as saver:
                graph=build_graph(saver)
                async with httpx.AsyncClient(base_url='http://go.test',transport=httpx.MockTransport(handle)) as client:
                    cfg={'configurable':{'thread_id':'d'*32}}
                    await graph.ainvoke({'execution_id':'d'*32},cfg,context=Bridge(client,'private'))
                    self.assertEqual((await graph.aget_state(cfg)).next,('wait_recovery',))
                    self.assertEqual(count,1)

if __name__=='__main__':unittest.main()
