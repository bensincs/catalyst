"""FastAPI application: REST API + embedded single-page UI."""

from __future__ import annotations

import logging
import time
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import Depends, FastAPI, HTTPException, status
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles
from sqlalchemy import select, text
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy.orm import Session

from .config import get_settings
from .db import Base, engine, get_session
from .models import Todo
from .schemas import TodoCreate, TodoRead, TodoUpdate

settings = get_settings()
logging.basicConfig(level=settings.log_level.upper())
logger = logging.getLogger("todoapp")

STATIC_DIR = Path(__file__).resolve().parent / "static"


def init_db() -> None:
    """Wait for the database to become reachable, then ensure the schema.

    Pods frequently start before the database is accepting connections, so we
    retry with a fixed backoff before giving up (which crash-loops the pod).
    """
    retries = max(1, settings.db_connect_retries)
    backoff = settings.db_connect_backoff_seconds
    last_error: Exception | None = None

    for attempt in range(1, retries + 1):
        try:
            with engine.connect() as conn:
                conn.execute(text("SELECT 1"))
            Base.metadata.create_all(bind=engine)
            logger.info("database ready; schema ensured")
            return
        except SQLAlchemyError as exc:
            last_error = exc
            logger.warning(
                "database not ready (attempt %d/%d): %s", attempt, retries, exc
            )
            if attempt < retries:
                time.sleep(backoff)

    raise RuntimeError("could not connect to the database") from last_error


@asynccontextmanager
async def lifespan(_app: FastAPI):
    init_db()
    yield


app = FastAPI(title="Todo App", version="0.1.0", lifespan=lifespan)


# --------------------------------------------------------------------------- #
# Health probes
# --------------------------------------------------------------------------- #
@app.get("/healthz", tags=["health"])
def healthz() -> dict[str, str]:
    """Liveness: the process is up. Deliberately does not touch the database."""
    return {"status": "ok"}


@app.get("/readyz", tags=["health"])
def readyz(session: Session = Depends(get_session)) -> dict[str, str]:
    """Readiness: verifies the database is reachable."""
    try:
        session.execute(text("SELECT 1"))
    except SQLAlchemyError as exc:  # pragma: no cover - defensive
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="database unavailable",
        ) from exc
    return {"status": "ready"}


# --------------------------------------------------------------------------- #
# Todo REST API
# --------------------------------------------------------------------------- #
@app.get("/api/todos", response_model=list[TodoRead], tags=["todos"])
def list_todos(session: Session = Depends(get_session)) -> list[Todo]:
    stmt = select(Todo).order_by(Todo.completed.asc(), Todo.created_at.desc(), Todo.id.desc())
    return list(session.scalars(stmt))


@app.post(
    "/api/todos",
    response_model=TodoRead,
    status_code=status.HTTP_201_CREATED,
    tags=["todos"],
)
def create_todo(payload: TodoCreate, session: Session = Depends(get_session)) -> Todo:
    todo = Todo(title=payload.title)
    session.add(todo)
    session.commit()
    session.refresh(todo)
    return todo


@app.get("/api/todos/{todo_id}", response_model=TodoRead, tags=["todos"])
def get_todo(todo_id: int, session: Session = Depends(get_session)) -> Todo:
    todo = session.get(Todo, todo_id)
    if todo is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="todo not found")
    return todo


@app.patch("/api/todos/{todo_id}", response_model=TodoRead, tags=["todos"])
def update_todo(
    todo_id: int, payload: TodoUpdate, session: Session = Depends(get_session)
) -> Todo:
    todo = session.get(Todo, todo_id)
    if todo is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="todo not found")

    data = payload.model_dump(exclude_unset=True)
    if data.get("title") is not None:
        todo.title = data["title"]
    if data.get("completed") is not None:
        todo.completed = data["completed"]

    session.commit()
    session.refresh(todo)
    return todo


@app.delete(
    "/api/todos/{todo_id}",
    status_code=status.HTTP_204_NO_CONTENT,
    tags=["todos"],
)
def delete_todo(todo_id: int, session: Session = Depends(get_session)) -> None:
    todo = session.get(Todo, todo_id)
    if todo is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="todo not found")
    session.delete(todo)
    session.commit()


# --------------------------------------------------------------------------- #
# Embedded UI
# --------------------------------------------------------------------------- #
app.mount("/static", StaticFiles(directory=str(STATIC_DIR)), name="static")


@app.get("/", include_in_schema=False)
def index() -> FileResponse:
    return FileResponse(STATIC_DIR / "index.html")
