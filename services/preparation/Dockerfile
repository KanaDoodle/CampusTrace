FROM python:3.14.7-slim-bookworm
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 LANGGRAPH_STRICT_MSGPACK=true LANGSMITH_TRACING=false
WORKDIR /app
COPY services/preparation/requirements.lock ./requirements.lock
RUN pip install --no-cache-dir --require-hashes -r requirements.lock \
    && useradd --uid 10001 --create-home agent \
    && mkdir -p /data && chown agent:agent /data
COPY services/preparation/app.py services/preparation/graph.py ./
USER agent
ENV PREPARATION_CHECKPOINT_DB=/data/checkpoints.sqlite
EXPOSE 18110
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "18110", "--workers", "1", "--no-access-log"]
