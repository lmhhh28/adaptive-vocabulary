"""Offline integration smoke check for the worker's locked dependencies."""
import importlib.metadata
import json
import os
from pathlib import Path
import platform
import sqlite3
import tempfile
from typing import TypedDict

# Must be set before importing the checkpoint serializer.
os.environ["LANGGRAPH_STRICT_MSGPACK"] = "true"
os.environ["LANGSMITH_TRACING"] = "false"

import httpx
from langgraph.checkpoint.sqlite import SqliteSaver
from langgraph.graph import END, START, StateGraph
from langgraph.types import Command, interrupt
from pydantic import BaseModel, ConfigDict, ValidationError


class State(TypedDict):
    value: int


class Candidate(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    value: int


def approval(state: State):
    delta = interrupt({"value": state["value"]})
    return {"value": state["value"] + delta}


def workflow(connection):
    graph = StateGraph(State)
    graph.add_node("approval", approval)
    graph.add_edge(START, "approval")
    graph.add_edge("approval", END)
    return graph.compile(checkpointer=SqliteSaver(connection))


def connect(path):
    connection = sqlite3.connect(path, check_same_thread=False)
    connection.execute("PRAGMA foreign_keys=ON")
    connection.execute("PRAGMA busy_timeout=5000")
    connection.execute("PRAGMA synchronous=FULL")
    if connection.execute("PRAGMA journal_mode=WAL").fetchone()[0] != "wal":
        raise RuntimeError("SQLite did not enter WAL mode")
    return connection


def main():
    if platform.python_version() != "3.13.15":
        raise RuntimeError("Unexpected Python version: " + platform.python_version())
    if sqlite3.sqlite_version_info < (3, 51, 3):
        raise RuntimeError("Unsafe SQLite runtime for the frozen WAL baseline: " + sqlite3.sqlite_version)
    with tempfile.TemporaryDirectory(prefix="avocab-checkpoint-") as directory:
        db_path = Path(directory) / "langgraph-checkpoints.db"
        config = {"configurable": {"thread_id": "dependency-check"}}
        first = connect(db_path)
        try:
            result = workflow(first).invoke({"value": 1}, config)
            if not result.get("__interrupt__"):
                raise RuntimeError("Expected durable interruption")
            source_id = first.execute("select sqlite_source_id()").fetchone()[0]
        finally:
            first.close()
        second = connect(db_path)
        try:
            resumed = workflow(second).invoke(Command(resume=2), config)
            if resumed["value"] != 3:
                raise RuntimeError("Checkpoint resume failed after reopening database")
            if second.execute("PRAGMA integrity_check").fetchone()[0] != "ok":
                raise RuntimeError("Checkpoint database failed integrity check")
        finally:
            second.close()
    with httpx.Client(transport=httpx.MockTransport(lambda request: httpx.Response(200, json={"value": 3}))) as client:
        result = Candidate.model_validate(client.get("https://fixture.invalid/candidate").json())
        if result.value != 3:
            raise RuntimeError("HTTP/JSON validation failed")
    try:
        Candidate.model_validate({"value": 3, "answer_key": "unexpected"})
    except ValidationError:
        pass
    else:
        raise RuntimeError("Unknown JSON fields were accepted")
    packages = {name: importlib.metadata.version(name) for name in ("langgraph", "langgraph-checkpoint-sqlite", "pydantic", "httpx")}
    print(json.dumps({"status": "ok", "python": platform.python_version(), "sqlite_version": sqlite3.sqlite_version,
                      "sqlite_source_id": source_id, "packages": packages, "checkpoint_reopen_resume": "ok", "external_requests": 0}))


if __name__ == "__main__":
    main()
