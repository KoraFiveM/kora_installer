"""Offline source bootstrap tests; no GitHub access or real Go builds."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


@unittest.skipIf(os.name == 'nt', 'POSIX bootstrap is for Linux/WSL')
class BootstrapTests(unittest.TestCase):
    def test_source_build_arguments_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            tools = root / 'tools'
            tools.mkdir()
            payload = b'#!/bin/sh\nprintf "%s\\n" "$@" > "$KORA_TEST_ARGS"\n'
            (root / 'payload').write_bytes(payload)
            scripts = {
                'git': '#!/bin/sh\nprintf "%s\\n" "$@" > "$KORA_TEST_SOURCE/git-args"\n[ "${FAIL_CLONE:-}" != 1 ] || exit 1\nfor arg in "$@"; do dest=$arg; done\nmkdir -p "$dest"\n',
                'go': '#!/bin/sh\n[ "${FAIL_BUILD:-}" != 1 ] || exit 1\nwhile [ "$1" != -o ]; do shift; done\nshift\ncp "$KORA_TEST_SOURCE/payload" "$1"\n',
            }
            for name, text in scripts.items():
                (tools / name).write_text(text)
                (tools / name).chmod(0o755)
            script = Path(__file__).resolve().parents[1] / 'install.sh'
            env = dict(os.environ, PATH=str(tools)+os.pathsep+os.environ['PATH'],
                       KORA_BIN_DIR=str(root/'bin'), KORA_REF='main',
                       KORA_TEST_SOURCE=str(root), KORA_TEST_ARGS=str(root/'args'))
            args = ['sh', str(script), '--yes', '--name=two words', '--license-key=DEMO_ONLY']
            result = subprocess.run(args, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root/'args').read_text().splitlines(), args[2:])
            self.assertIn('main', (root/'git-args').read_text().splitlines())
            self.assertNotEqual(subprocess.run(args, env=env, capture_output=True).returncode, 0)
            self.assertEqual((root/'bin/kora').read_bytes(), payload)
            for setting in ['FAIL_CLONE', 'FAIL_BUILD']:
                failing = dict(env, KORA_BIN_DIR=str(root/setting), **{setting: '1'})
                self.assertNotEqual(subprocess.run(args, env=failing, capture_output=True).returncode, 0)
                self.assertFalse((root/setting/'kora').exists())
            invalid = dict(env, KORA_BIN_DIR=str(root/'invalid'), KORA_REF='--upload-pack=bad')
            self.assertNotEqual(subprocess.run(args, env=invalid, capture_output=True).returncode, 0)
            pinned = dict(env, KORA_BIN_DIR=str(root/'pinned'), KORA_REF='feature/demo')
            self.assertEqual(subprocess.run(['sh', str(script), '--install-only'], env=pinned, capture_output=True).returncode, 0)
            self.assertIn('feature/demo', (root/'git-args').read_text().splitlines())
