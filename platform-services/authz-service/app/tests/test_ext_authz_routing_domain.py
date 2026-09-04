from __future__ import annotations

import importlib

import pytest


def _rebuild_module_with_domain(monkeypatch: pytest.MonkeyPatch, domain: str):
    """Reload routes.ext_authz with ROUTING_DOMAIN set so the import-time
    hostname pattern is rebuilt from the supplied apex."""
    monkeypatch.setenv("ROUTING_DOMAIN", domain)
    import routes.ext_authz as mod

    return importlib.reload(mod)


class TestRoutingDomainConfigurable:
    """The ext-authz hostname pattern must follow ROUTING_DOMAIN so the routing
    apex can be switched per environment without a code change. A mismatch here
    denies every tenant request with a 403, so this is the guard for that."""

    def test_requires_routing_domain_when_unset(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        monkeypatch.delenv("ROUTING_DOMAIN", raising=False)

        with pytest.raises(RuntimeError, match="ROUTING_DOMAIN is not set"):
            import routes.ext_authz as mod

            importlib.reload(mod)

    def test_accepts_configured_domain(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        mod = _rebuild_module_with_domain(monkeypatch, "inception42.ai")
        assert mod._parse_hostname("my-app.acme.inception42.ai") == (
            "my-app",
            "acme",
        )

    def test_rejects_old_domain_after_switch(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        mod = _rebuild_module_with_domain(monkeypatch, "inception42.ai")
        assert mod._parse_hostname("my-app.acme.cortex.ai") is None

    def test_accepts_env_suffixed_apex(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        mod = _rebuild_module_with_domain(monkeypatch, "int.inception42.ai")
        assert mod._parse_hostname("cortex-ui.acme.int.inception42.ai") == (
            "cortex-ui",
            "acme",
        )

    def test_preserves_optional_port(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        mod = _rebuild_module_with_domain(monkeypatch, "inception42.ai")
        assert mod._parse_hostname("my-app.acme.inception42.ai:8443") == (
            "my-app",
            "acme",
        )

    def test_domain_is_regex_escaped(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        # The dots in the apex must be literal, not regex wildcards, so a
        # look-alike host with a different separator is rejected.
        mod = _rebuild_module_with_domain(monkeypatch, "inception42.ai")
        assert mod._parse_hostname("my-app.acme.inception42Xai") is None


@pytest.fixture(autouse=True)
def _restore_module(monkeypatch: pytest.MonkeyPatch):
    """Reload the module once more after each test with the test env restored so
    the mutated import-time pattern does not leak into other test modules."""
    yield
    monkeypatch.setenv("ROUTING_DOMAIN", "cortex.ai")
    import routes.ext_authz as mod

    importlib.reload(mod)
