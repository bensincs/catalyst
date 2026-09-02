"""End-to-end API tests using FastAPI's TestClient against SQLite."""

from __future__ import annotations


def test_healthz(client):
    resp = client.get("/healthz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


def test_readyz(client):
    resp = client.get("/readyz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ready"}


def test_index_is_served(client):
    resp = client.get("/")
    assert resp.status_code == 200
    assert "text/html" in resp.headers["content-type"]
    assert "todo" in resp.text.lower()


def test_list_starts_empty(client):
    resp = client.get("/api/todos")
    assert resp.status_code == 200
    assert resp.json() == []


def test_full_crud_flow(client):
    # create
    resp = client.post("/api/todos", json={"title": "buy milk"})
    assert resp.status_code == 201
    todo = resp.json()
    assert todo["title"] == "buy milk"
    assert todo["completed"] is False
    todo_id = todo["id"]

    # list contains it
    resp = client.get("/api/todos")
    assert len(resp.json()) == 1

    # fetch by id
    resp = client.get(f"/api/todos/{todo_id}")
    assert resp.status_code == 200

    # mark complete
    resp = client.patch(f"/api/todos/{todo_id}", json={"completed": True})
    assert resp.status_code == 200
    assert resp.json()["completed"] is True

    # rename
    resp = client.patch(f"/api/todos/{todo_id}", json={"title": "buy oat milk"})
    assert resp.status_code == 200
    assert resp.json()["title"] == "buy oat milk"

    # delete
    resp = client.delete(f"/api/todos/{todo_id}")
    assert resp.status_code == 204

    # gone
    resp = client.get(f"/api/todos/{todo_id}")
    assert resp.status_code == 404


def test_create_requires_non_empty_title(client):
    resp = client.post("/api/todos", json={"title": ""})
    assert resp.status_code == 422


def test_update_missing_returns_404(client):
    resp = client.patch("/api/todos/999999", json={"completed": True})
    assert resp.status_code == 404


def test_delete_missing_returns_404(client):
    resp = client.delete("/api/todos/999999")
    assert resp.status_code == 404
