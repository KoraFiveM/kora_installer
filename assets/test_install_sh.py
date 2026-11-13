"""Offline bootstrap tests with fake downloaded binaries."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

@unittest.skipIf(os.name == 'nt', 'POSIX bootstrap is for Linux/WSL')
class BootstrapTests(unittest.TestCase):
    def test_checksum_install_arguments_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory); tools=root/'tools';tools.mkdir()
            payload=b'#!/bin/sh\nprintf "%s\\n" "$@" > "$KORA_TEST_ARGS"\n'
            (root/'payload').write_bytes(payload)
            (root/'sum').write_text(hashlib.sha256(payload).hexdigest()+'  kora-linux-amd64\n')
            curl=tools/'curl'
            curl.write_text('#!/bin/sh\nfor arg in "$@"; do case "$arg" in https:*) url=$arg;; esac; done\nwhile [ "$1" != -o ]; do shift; done\nshift\ncase "$url" in *.sha256) cp "$KORA_TEST_SOURCE/sum" "$1";; *) cp "$KORA_TEST_SOURCE/payload" "$1";; esac\n')
            curl.chmod(0o755)
            script=Path(__file__).resolve().parents[1]/'install.sh'
            env=dict(os.environ,PATH=str(tools)+os.pathsep+os.environ['PATH'],KORA_BIN_DIR=str(root/'bin'),KORA_TEST_SOURCE=str(root),KORA_TEST_ARGS=str(root/'args'))
            args=['sh',str(script),'--yes','--name=two words','--license-key=DEMO_ONLY']
            result=subprocess.run(args,env=env,capture_output=True,text=True)
            self.assertEqual(result.returncode,0,result.stderr)
            self.assertEqual((root/'args').read_text().splitlines(),args[2:])
            self.assertNotEqual(subprocess.run(args,env=env,capture_output=True).returncode,0)
            self.assertEqual((root/'bin/kora').read_bytes(),payload)
            (root/'sum').write_text('0'*64+'  kora-linux-amd64\n')
            env['KORA_BIN_DIR']=str(root/'bad-bin')
            self.assertNotEqual(subprocess.run(args,env=env,capture_output=True).returncode,0)
            self.assertFalse((root/'bad-bin/kora').exists())
