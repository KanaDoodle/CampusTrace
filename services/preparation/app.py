"""Internal service. Go is authoritative for users, budgets and business writes."""
import asyncio
import contextlib
import hmac
import importlib.metadata
import os
from contextlib import asynccontextmanager
from pathlib import Path

os.environ.setdefault('LANGGRAPH_STRICT_MSGPACK', 'true')
os.environ['LANGSMITH_TRACING'] = 'false'
os.environ['LANGCHAIN_TRACING_V2'] = 'false'

import httpx
from fastapi import FastAPI, Header, HTTPException, Request
from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
from langgraph.types import Command
from pydantic import BaseModel, ConfigDict, Field
from graph import Bridge, GRAPH_VERSION, build_graph

class Run(BaseModel):
    model_config = ConfigDict(extra='forbid')
    execution_id: str = Field(pattern=r'^[a-f0-9]{32}$')
    parent_id: str = Field(default='', pattern=r'^(?:[a-f0-9]{32})?$')
    sequence: int = Field(ge=0)

@asynccontextmanager
async def lifespan(app):
    path = Path(os.getenv('PREPARATION_CHECKPOINT_DB', 'var/preparation/checkpoints.sqlite')).resolve()
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    async with AsyncSqliteSaver.from_conn_string(str(path)) as saver:
        await saver.setup()
        await saver.conn.execute('PRAGMA journal_mode=WAL')
        await saver.conn.execute('PRAGMA busy_timeout=5000')
        os.chmod(path, 0o600)
        app.state.graph = build_graph(saver)
        # A single process owns the SQLite file. Each thread is single-flight;
        # Go's SQL lease independently fences old workers across restarts.
        app.state.active = set()
        app.state.limit = asyncio.Semaphore(4)
        async with httpx.AsyncClient(base_url=os.getenv('PREPARATION_GO_URL', 'http://127.0.0.1:8080'),
                                     timeout=36, trust_env=False) as client:
            app.state.client = client
            yield

app = FastAPI(lifespan=lifespan, docs_url=None, redoc_url=None, openapi_url=None)

def config(thread):
    return {'configurable': {'thread_id': thread}, 'recursion_limit': 48}

@app.get('/healthz')
async def health():
    # A real file-backed checkpoint query also checks database availability.
    await app.state.graph.aget_state(config('health'))
    return {'status': 'ready', 'executor': 'langgraph', 'graph_version': GRAPH_VERSION, 'langgraph_version': importlib.metadata.version('langgraph'), 'checkpointer_version': importlib.metadata.version('langgraph-checkpoint-sqlite')}

@app.post('/run')
async def run(body: Run, request: Request, authorization: str = Header(default=''),
              x_execution_capability: str = Header(default='')):
    token = os.getenv('PREPARATION_SERVICE_TOKEN', 'local-preparation-service-change-this-2027')
    if not hmac.compare_digest(authorization, 'Bearer ' + token) or len(x_execution_capability) != 64:
        raise HTTPException(401, 'invalid service capability')
    active = app.state.active
    if body.execution_id in active:
        raise HTTPException(409, 'thread already running')
    if len(active) >= 4:
        raise HTTPException(503, 'preparation capacity reached')
    active.add(body.execution_id)
    bridge = Bridge(app.state.client, x_execution_capability)
    graph = app.state.graph

    async def execute():
        async with app.state.limit:
            # A clarification is resumed with the reviewed child revision ID.
            # Its wait node has no I/O and cannot write to the old Go execution.
            if body.parent_id:
                parent = await graph.aget_state(config(body.parent_id))
                if parent.next == ('wait_input',):
                    await graph.ainvoke(Command(resume={'execution_id': body.execution_id}),
                                        config(body.parent_id), context=bridge)
            cfg = config(body.execution_id)
            saved = await graph.aget_state(cfg)
            if not saved.values:
                arg = {'execution_id': body.execution_id, 'parent_id': body.parent_id}
            elif saved.next == ('wait_recovery',):
                # Go has already checked retry_unknown, material versions and
                # acquired a new lease. The sync operation cannot dispatch a model.
                response = await bridge.client.post('/internal/preparation/step',
                    headers={'Authorization': 'Bearer ' + bridge.capability},
                    json={'action': 'sync', 'sequence': body.sequence})
                response.raise_for_status()
                arg = Command(resume={'view': response.json()})
            elif not saved.next:
                # A known terminal graph must not restart from START.
                return {'status': 'checkpoint_complete', 'graph_version': GRAPH_VERSION}
            else:
                arg = None  # durable pending node; its Go receipt makes replay safe
            result = await graph.ainvoke(arg, cfg, context=bridge, durability='sync')
            return {'status': 'interrupted' if result.get('__interrupt__') else 'complete',
                    'last_node': result.get('last_node'), 'graph_version': GRAPH_VERSION}

    task = asyncio.create_task(execute())
    async def disconnected():
        while not await request.is_disconnected():
            await asyncio.sleep(.1)
    watch = asyncio.create_task(disconnected())
    try:
        done, _ = await asyncio.wait([task, watch], return_when=asyncio.FIRST_COMPLETED)
        if watch in done and not task.done():
            task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await task
            raise HTTPException(499, 'Go caller disconnected; execution stopped')
        return await task
    except httpx.HTTPError:
        # Never expose provider responses or capability headers in logs/errors.
        raise HTTPException(502, 'Go operation unavailable or execution fenced') from None
    finally:
        watch.cancel()
        active.discard(body.execution_id)
