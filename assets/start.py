#!/usr/bin/env python3
"""Kora Framework development launcher; never evaluates the environment as shell code."""
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
from urllib.parse import quote

ROOT = Path(__file__).resolve().parent


def write_new(path, text):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as stream:
        stream.write(text)


def read_env():
    values = {}
    for line in (ROOT / ".env").read_text(encoding="utf-8").splitlines():
        if not line or line.startswith("#"):
            continue
        key, value = line.split("=", 1)
        values[key] = json.loads(value)
        if not isinstance(values[key], str):
            raise ValueError("Environment values must be JSON strings")
    return values


def cfg_value(value):
    if any(ord(char) < 32 or char in '\\"' for char in value):
        raise ValueError("Unsupported character in configuration value")
    return value


def launch():
    if not (ROOT / "install.complete").is_file():
        raise RuntimeError("Installation is incomplete; retained files are for inspection only")
    values = read_env()
    stage = "reading configuration"
    inside = "--inside-container" in sys.argv
    if values["DOCKER"] == "true" and not inside:
        environment = dict(os.environ, **values)
        # The explicit empty env file prevents Compose interpreting the private
        # JSON-string dotenv dialect or interpolating characters in passwords.
        return subprocess.call(
            ["docker", "compose", "--env-file", ".compose-env", "up", "--build"],
            cwd=ROOT, env=environment,
        )

    print("[KORA] Preparing a private server session", flush=True)
    runtime = ROOT / "runtime"
    runtime.mkdir(mode=0o700, exist_ok=True)
    previous = sorted(
        (p for p in runtime.glob("session-*") if (p / "ready").is_file()),
        key=lambda p: p.stat().st_mtime_ns,
    )
    session = Path(tempfile.mkdtemp(prefix="session-", dir=runtime))
    scheme = "mysql"
    host = values["DB_HOST"]
    if ":" in host:
        host = "[" + host + "]"
    connection = (
        f"{scheme}://{quote(values['DB_USER'], safe='')}:{quote(values['DB_PASSWORD'], safe='')}"
        f"@{host}:{int(values['DB_PORT'])}/{quote(values['DB_NAME'], safe='')}?charset=utf8mb4"
    )
    credentials = session / "credentials.cfg"
    write_new(credentials,
              f'sv_licenseKey "{cfg_value(values["CFX_LICENSE_KEY"])}"\n'
              f'set mysql_connection_string "{cfg_value(connection)}"\n')
    relative = "../" + credentials.relative_to(ROOT).as_posix()
    write_new(session / "server.cfg", f'exec "{relative}"\nexec server.cfg\n')
    environment = dict(os.environ)
    environment["TXHOST_DATA_PATH"] = str(session / "txData")
    environment["TXHOST_TXA_PORT"] = str(int(values["TXADMIN_PORT"]))
    environment["TXHOST_GAME_NAME"] = "fivem"
    executable = ROOT / "artifacts" / ("FXServer.exe" if os.name == "nt" else "run.sh")
    if inside:
        executable = Path("/opt/fxserver/run.sh")
    args = [str(executable), "+set", "onesync", "on"]
    if values["TXADMIN_ENABLED"] == "true":
        # Preserve txAdmin accounts/state by copying the last session into a NEW
        # directory. Never edit the previous profile or its runtime credentials.
        if previous and (previous[-1] / "txData").is_dir():
            def omit_config(directory, names):
                return ["config.json"] if Path(directory).name == "default" else []
            shutil.copytree(previous[-1] / "txData", session / "txData", ignore=omit_config)
        profile = session / "txData" / "default"
        profile.mkdir(parents=True, mode=0o700, exist_ok=True)
        config = {}
        if previous:
            old_config = previous[-1] / "txData" / "default" / "config.json"
            if old_config.is_file():
                config = json.loads(old_config.read_text(encoding="utf-8"))
        config.setdefault("version", 2)
        config.setdefault("general", {}).setdefault("serverName", "Kora")
        config.setdefault("server", {}).update({
            "dataPath": str(ROOT / "server-data"),
            "cfgPath": "../" + (session / "server.cfg").relative_to(ROOT).as_posix(),
            "onesync": "on", "autoStart": True,
        })
        write_new(profile / "config.json", json.dumps(config, indent=2))

    else:
        args += ["+exec", "../" + (session / "server.cfg").relative_to(ROOT).as_posix()]
    print(f"[KORA] Waiting for database at {values['DB_HOST']}:{values['DB_PORT']}", flush=True)
    deadline = time.monotonic() + 90
    while True:
        try:
            with socket.create_connection((values["DB_HOST"], int(values["DB_PORT"])), timeout=2):
                break
        except OSError:
            if time.monotonic() >= deadline:
                raise RuntimeError("Database did not become reachable within 90 seconds")
            time.sleep(1)
    print("[KORA] Database reachable. Launching FXServer", flush=True)
    write_new(session / "ready", "ready\n")
    return subprocess.call(args, cwd=ROOT / "server-data", env=environment)


if __name__ == "__main__":
    try:
        sys.exit(launch())
    except KeyboardInterrupt:
        sys.exit(130)
    except Exception as error:
        # Redact values before showing a useful error. JSON parsing errors do not
        # include input content, but still mask as a precaution.
        detail = str(error)
        try:
            private = read_env()
            for key in ("DB_PASSWORD", "DB_ROOT_PASSWORD", "CFX_LICENSE_KEY"):
                value = private.get(key, "")
                if value:
                    detail = detail.replace(value, "[redacted]")
        except Exception:
            detail = type(error).__name__ + " while reading .env"
        print(f"[KORA] Startup failed: {detail}", file=sys.stderr)
        sys.exit(1)
