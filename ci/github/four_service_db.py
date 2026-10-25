#!/usr/bin/env python3
"""Run and enforce the mandatory four-service disposable database suites."""

from __future__ import annotations

import json
import os
import subprocess
import sys


EXPECTED = {
    "github.com/syuhei0519/backend-app/internal/store/postgres": {
        "TestDisposableAccountMigration",
    },
    "github.com/syuhei0519/backend-app/internal/store/orderpostgres": {
        "TestDisposableMigration",
        "TestDisposableTargetGuard",
    },
    "github.com/syuhei0519/backend-app/internal/store/paymentpostgres": {
        "TestDisposablePaymentMigration",
    },
    "github.com/syuhei0519/backend-app/internal/store/notificationpostgres": {
        "TestDisposableNotificationMigration",
    },
    "github.com/syuhei0519/backend-app/tests/paymentintegration": {
        "TestDisposablePaymentLifecycle",
    },
    "github.com/syuhei0519/backend-app/tests/notificationintegration": {
        "TestOwnedLifecycle",
    },
    "github.com/syuhei0519/backend-app/tests/orderintegration": {
        "TestDisposableHTTPPersistenceAndAccount",
    },
    "github.com/syuhei0519/backend-app/tests/orderpaymentintegration": {
        "TestOrderPaymentLifecycle",
    },
}


def evaluate(lines: list[str], returncode: int) -> list[str]:
    terminal: dict[tuple[str, str], str] = {}
    errors: list[str] = []
    for raw in lines:
        try:
            event = json.loads(raw)
        except json.JSONDecodeError:
            continue
        package, test, action = event.get("Package"), event.get("Test"), event.get("Action")
        if package in EXPECTED and test in EXPECTED[package] and action in {"pass", "fail", "skip"}:
            terminal[(package, test)] = action
    for package, tests in EXPECTED.items():
        for test in tests:
            action = terminal.get((package, test))
            if action != "pass":
                errors.append(f"{package}:{test} expected pass, got {action or 'unrun'}")
    if returncode != 0:
        errors.append(f"go test exited {returncode}")
    return errors


def main() -> int:
    names = sorted({test for tests in EXPECTED.values() for test in tests})
    packages = list(EXPECTED)
    command = [
        os.environ.get("GO_BINARY", "go"), "test", "-json", "-count=1", "-p=6",
        "-run", "^(" + "|".join(names) + ")$", *packages,
    ]
    env = os.environ.copy()
    env["ORDER_DISPOSABLE_TEST"] = "1"
    env["ORDER_PAYMENT_DISPOSABLE_TEST"] = "1"
    process = subprocess.Popen(command, stdout=subprocess.PIPE, text=True, env=env)
    assert process.stdout is not None
    lines = []
    for line in process.stdout:
        lines.append(line)
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("Action") in {"pass", "fail", "skip"} and event.get("Test") in {
            test for tests in EXPECTED.values() for test in tests
        }:
            print(f"{event['Action'].upper()} {event['Package']}:{event['Test']}", flush=True)
    returncode = process.wait()
    errors = evaluate(lines, returncode)
    if errors:
        for error in errors:
            print(f"ERROR {error}", file=sys.stderr)
        return 1
    print(f"PASS mandatory disposable DB suites={sum(map(len, EXPECTED.values()))}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
