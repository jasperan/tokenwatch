# endpoints.json

Real JSON captured from TokenWatch's own dashboard app, not hand-written.

Produced by running `create_dashboard_app()` from
`src/tokenwatch/dashboard_app.py` under the repo's `.venv` with a fake Database
(mirroring `tests/test_dashboard_app.py`'s monkeypatch), then recording each
response verbatim. The fake deliberately returns `Decimal` values so the capture
shows how Oracle NUMBER columns are actually encoded.

Regenerate with the script in that procedure; do not hand-edit. `client_test.go`
serves these exact bodies so a shape change in db.py fails this suite instead of
silently breaking the TUI.
