"""Exercise the CI publishing gates without building or publishing artifacts."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
VERSION = json.loads((ROOT / 'package.json').read_text())['version']


class ReleaseGateTest(unittest.TestCase):
    def build(self, **overrides):
        with tempfile.TemporaryDirectory(prefix='brote-release-gate-') as directory:
            root = Path(directory)
            call = root / 'build.json'
            python = root / 'python3'
            python.write_text(
                f'#!{sys.executable}\n'
                'import json, os, pathlib, sys\n'
                'pathlib.Path(os.environ["BUILD_CALL"]).write_text(json.dumps(sys.argv[1:]))\n'
            )
            python.chmod(0o755)
            env = {
                **os.environ,
                'PATH': str(root) + os.pathsep + os.environ['PATH'],
                'GITHUB_REPOSITORY': 'javiermolinar/Brote',
                'RELEASE_TAG': 'v' + VERSION,
                'VSCODE_PUBLISH_ENABLED': '',
                'VSCODE_PUBLISHER': '',
                'BROTE_NPM_NAME': '',
                'BUILD_CALL': str(call),
                **overrides,
            }
            result = subprocess.run(['sh', 'scripts/build-release.sh'], cwd=ROOT,
                                    env=env, capture_output=True, text=True)
            return result, json.loads(call.read_text()) if call.exists() else None

    def test_github_release_needs_no_registry_identities(self):
        result, call = self.build()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(call, ['scripts/release.py', '--version', VERSION,
                               '--repository', 'javiermolinar/Brote',
                               '--npm-name', 'brote-pi'])

    def test_manual_build_needs_no_tag_or_registry_identities(self):
        result, call = self.build(RELEASE_TAG='')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIsNotNone(call)

    def test_marketplace_requires_publisher_only_when_enabled(self):
        for publisher in ['', 'brote-local']:
            with self.subTest(publisher=publisher):
                result, call = self.build(VSCODE_PUBLISH_ENABLED='true',
                                          VSCODE_PUBLISHER=publisher)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('Set VSCODE_PUBLISHER', result.stderr)
                self.assertIsNone(call)

    def test_marketplace_does_not_require_npm_publication(self):
        result, call = self.build(VSCODE_PUBLISH_ENABLED='true',
                                  VSCODE_PUBLISHER='brote-test-publisher')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(call[-2:], ['--npm-name', 'brote-pi'])

    def test_custom_npm_package_name_is_preserved(self):
        result, call = self.build(BROTE_NPM_NAME='@example/brote')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(call[-2:], ['--npm-name', '@example/brote'])

    def test_mismatched_tag_never_builds(self):
        result, call = self.build(RELEASE_TAG='v0.0.0-wrong')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Release tag must match', result.stderr)
        self.assertIsNone(call)


if __name__ == '__main__':
    unittest.main()
