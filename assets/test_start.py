"""Offline tests: no Docker, FXServer, or databases are launched."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch, MagicMock

spec = importlib.util.spec_from_file_location("launcher", Path(__file__).with_name("start.py"))
launcher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(launcher)


class LaunchTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        launcher.ROOT = self.root
        self.values = dict(DOCKER="false", DB_HOST="127.0.0.1", DB_PORT="3306",
                           DB_NAME="fresh", DB_USER="fresh", DB_PASSWORD='p$"\\ss',
                           CFX_LICENSE_KEY="test-key", TXADMIN_ENABLED="false",
                           TXADMIN_PORT="40120")
        (self.root / "install.complete").write_text("complete")
        (self.root / "server-data").mkdir()
        (self.root / ".env").write_text("\n".join(k + "=" + json.dumps(v) for k, v in self.values.items()))

    def test_env_round_trip_and_no_overwrite(self):
        self.assertEqual(launcher.read_env(), self.values)
        target = self.root / "private.cfg"
        launcher.write_new(target, "original")
        with self.assertRaises(FileExistsError):
            launcher.write_new(target, "replacement")
        self.assertEqual(target.read_text(), "original")

    def test_sessions_keep_secrets_off_command_line(self):
        with patch.object(launcher.socket, "create_connection", return_value=MagicMock()), patch.object(launcher.subprocess, "call", return_value=0) as call:
            launcher.launch()
            first = list((self.root / "runtime").glob("session-*"))[0]
            original = (first / "credentials.cfg").read_text()
            launcher.launch()
            self.assertEqual(len(list((self.root / "runtime").glob("session-*"))), 2)
            self.assertEqual((first / "credentials.cfg").read_text(), original)
            self.assertNotIn("test-key", str(call.call_args))
            self.assertNotIn(self.values["DB_PASSWORD"], str(call.call_args))
            self.assertIn("p%24%22%5Css", original)

    def test_txadmin_copies_state_without_overwriting_previous(self):
        self.values["TXADMIN_ENABLED"] = "true"
        with patch.object(launcher, "read_env", return_value=self.values), patch.object(launcher.socket, "create_connection", return_value=MagicMock()), patch.object(launcher.subprocess, "call", return_value=0):
            launcher.launch()
            first = list((self.root / "runtime").glob("session-*"))[0]
            config = first / "txData/default/config.json"
            original = config.read_text()
            (first / "txData/admins.json").write_text('{"test":"account"}')
            launcher.launch()
            second = next(p for p in (self.root / "runtime").glob("session-*") if p != first)
            self.assertEqual(config.read_text(), original)
            self.assertEqual((second / "txData/admins.json").read_text(), '{"test":"account"}')
            self.assertIn(second.name, (second / "txData/default/config.json").read_text())

    def test_incomplete_install_wont_start(self):
        (self.root / "install.complete").unlink()
        with self.assertRaises(RuntimeError), patch.object(launcher.subprocess, "call") as call:
            launcher.launch()
        call.assert_not_called()

    def test_docker_passes_decoded_env(self):
        self.values["DOCKER"] = "true"
        with patch.object(launcher, "read_env", return_value=self.values), patch.object(launcher.subprocess, "call", return_value=0) as call:
            launcher.launch()
        self.assertEqual(call.call_args.kwargs["env"]["DB_PASSWORD"], self.values["DB_PASSWORD"])
        self.assertIn(".compose-env", call.call_args.args[0])


if __name__ == "__main__":
    unittest.main()
