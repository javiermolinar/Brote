"""Smoke the actual release artifacts without changing the user's installation.

Set AGENTDEBUGGER_RELEASE_DIR to a directory produced by release.py.
Host registrations use stubs; binaries, archive contents and HTTP UI are real.
"""
import hashlib
import json
import os
import platform
import subprocess
import tarfile
import tempfile
import time
import unittest
import urllib.request
import zipfile
from pathlib import Path


@unittest.skipUnless(os.environ.get('AGENTDEBUGGER_RELEASE_DIR'), 'build release and set AGENTDEBUGGER_RELEASE_DIR')
class ReleaseTest(unittest.TestCase):
    def test_installable_packages_and_upgrade(self):
        release = Path(os.environ['AGENTDEBUGGER_RELEASE_DIR']).resolve()
        manifest = json.loads((release / 'release-manifest.json').read_text())
        version = manifest['version']
        system = 'darwin' if platform.system() == 'Darwin' else 'linux'
        arch = 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'
        target = system + '-' + ('x64' if arch == 'amd64' else arch)
        for line in (release / 'SHA256SUMS').read_text().splitlines():
            digest, name = line.split('  ', 1)
            self.assertEqual(hashlib.sha256((release / name).read_bytes()).hexdigest(), digest)
        for name in manifest['artifacts']:
            self.assertTrue((release / name).is_file(), name)
        with tempfile.TemporaryDirectory(prefix='brote-smoke-') as directory:
            root = Path(directory)
            env = {**os.environ, 'HOME': str(root / 'home'), 'DELVE_LLM_ADAPTER_HOME': str(root / 'install'),
                   'DEBUG_HANDOVER_HOME': str(root / 'sessions'), 'AGENTDEBUGGER_DATA_DIR': str(root / 'data'),
                   'CODEX_THREAD_ID': '', 'BROTE_BIN': '', 'AGENTDEBUGGER_BIN': '', 'DELVE_LLM_ADAPTER_BIN': ''}
            Path(env['HOME']).mkdir()
            npm_name = manifest['npm'].replace('@', '').replace('/', '-')
            with tarfile.open(release / f'{npm_name}-{version}.tgz') as archive:
                archive.extractall(root / 'pi', filter='data')
            pi = root / 'pi/package'
            package = json.loads((pi / 'package.json').read_text())
            self.assertEqual(package['pi']['extensions'], ['./index.js'])
            self.assertTrue((pi / 'skills/debug-handover/SKILL.md').is_file())
            self.assertTrue((pi / 'assets/brote-plant.png').is_file())
            self.assertIn('src="assets/brote-plant.png"', (pi / 'README.md').read_text())
            self.assertNotIn('../../packages/', (pi / 'index.js').read_text())
            for item in manifest['platforms']:
                folder = item.replace('/amd64', '-x64').replace('/', '-')
                self.assertTrue(os.access(pi / 'runtime' / folder / 'brote', os.X_OK))
            with zipfile.ZipFile(release / f'brote-codex-{version}.zip') as archive:
                archive.extractall(root / 'codex')
                for file in archive.infolist():
                    if not file.is_dir(): (root / 'codex' / file.filename).chmod(file.external_attr >> 16 & 0o777)
            plugin = root / 'codex/brote'
            self.assertEqual(json.loads((plugin / '.codex-plugin/plugin.json').read_text())['version'], version)
            self.assertTrue((plugin / 'assets/brote-plant.png').is_file())
            for item in manifest['platforms']:
                folder = item.replace('/amd64', '-x64').replace('/', '-')
                with zipfile.ZipFile(release / f'brote-{version}-{folder}.vsix') as archive:
                    self.assertIn('extension/bin/brote', archive.namelist())
                    self.assertIn('extension/assets/brote-plant.png', archive.namelist())
                    self.assertIn(f'TargetPlatform="{folder}"', archive.read('extension.vsixmanifest').decode())
                    ext_manifest = json.loads(archive.read('extension/package.json'))
                    self.assertEqual(ext_manifest['publisher'] + '.' + ext_manifest['name'], manifest['vscode'])
                    if folder == target:
                        archive.extractall(root / 'vscode')
                        info = archive.getinfo('extension/bin/brote')
                        (root / 'vscode/extension/bin/brote').chmod(info.external_attr >> 16 & 0o777)
            with tarfile.open(release / f'brote-v{version}-{system}-{arch}.tar.gz') as archive:
                archive.extractall(root, filter='data')
            bundle = root / 'delve-llm-adapter'
            self.assertTrue((bundle / 'assets/debugger-conversation.png').is_file())
            self.assertIn('(assets/debugger-conversation.png)', (bundle / 'README.md').read_text())
            core = bundle / 'bin/brote'
            def run(binary, *args):
                result = subprocess.run([str(binary), *args], env=env, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
                return json.loads(result.stdout)
            for binary in [core, bundle / 'bin/agentdebugger', bundle / 'bin/delve-llm-adapter', bundle / 'bin/debug-handover', pi / 'runtime' / target / 'brote', plugin / 'scripts/debug-handover', root / 'vscode/extension/bin/brote']:
                info = run(binary, 'version')
                self.assertEqual(info['version'], version)
                self.assertIn('taskExecute', info['capabilities'])
            # Optional real host registration, always in fresh profile directories.
            code = os.environ.get('AGENTDEBUGGER_TEST_CODE')
            if code:
                profile_args = ['--user-data-dir', str(root / 'code-user'), '--extensions-dir', str(root / 'code-extensions')]
                vsix = release / f'brote-{version}-{target}.vsix'
                result = subprocess.run([code, *profile_args, '--install-extension', str(vsix), '--force'], env=env, capture_output=True, text=True, timeout=60)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                result = subprocess.run([code, *profile_args, '--list-extensions', '--show-versions'], env=env, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(manifest['vscode'] + '@' + version, result.stdout)
            pi_host = os.environ.get('AGENTDEBUGGER_TEST_PI')
            if pi_host:
                pi_env = {**env, 'PI_CODING_AGENT_DIR': str(root / 'pi-agent')}
                for args in [['install', str(pi)], ['list'], ['remove', str(pi)]]:
                    result = subprocess.run([pi_host, *args], cwd=root, env=pi_env, capture_output=True, text=True, timeout=30)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    if args == ['list']:
                        self.assertIn(str(pi), result.stdout)
                        # Read host metadata only: no prompt, model request or target launch.
                        result = subprocess.run([pi_host, '--mode', 'rpc', '--no-session'], input='{"type":"get_commands","id":"smoke"}\n', cwd=root, env=pi_env, capture_output=True, text=True, timeout=30)
                        self.assertEqual(result.returncode, 0, result.stderr)
                        responses = [json.loads(line) for line in result.stdout.splitlines() if line.startswith('{')]
                        response = next((r for r in responses if r.get('id') == 'smoke'), None)
                        self.assertIsNotNone(response, result.stdout + result.stderr)
                        self.assertTrue(response['success'], response)
                        self.assertIn('debug-connect', [c['name'] for c in response['data']['commands']])
            stubs = root / 'stubs'
            stubs.mkdir()
            calls = root / 'calls'
            for name in ['code', 'codex', 'pi']:
                file = stubs / name
                file.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$SMOKE_CALLS"\n')
                file.chmod(0o755)
            env.update(PATH=str(stubs) + ':/usr/bin:/bin', SMOKE_CALLS=str(calls))
            # Exercise the user's single-command shell path against real artifacts.
            installer = Path(__file__).resolve().parent.parent / 'install.sh'
            result = subprocess.run(['sh', str(installer), '--from', str(release), '--agent', 'pi', '--editor', 'vscode'], env=env, capture_output=True, text=True, timeout=60)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn('/reload', result.stdout)
            run(core, 'setup', '--bundle', str(bundle), '--agent', 'codex')
            run(core, 'repair')
            installed = run(core, 'installation')['installation']
            self.assertEqual(installed['vscodeExtension'], manifest['vscode'])
            self.assertEqual(set(installed['components'].values()), {'installed'})
            # A versioned installation remains intact after updating current.
            old_path = (root / 'install/current/bin/brote').resolve()
            release_json = json.loads((bundle / 'release.json').read_text())
            release_json['version'] = version + '-upgrade-test'
            (bundle / 'release.json').write_text(json.dumps(release_json))
            saved = root / 'sessions/0123456789/session.json'
            saved.parent.mkdir(parents=True)
            saved.write_text(json.dumps({'id': '0123456789', 'pid': os.getpid()}))
            before = saved.read_bytes()
            run(core, 'setup', '--bundle', str(bundle))
            self.assertNotEqual(old_path, (root / 'install/current/bin/brote').resolve())
            self.assertTrue(old_path.is_file())
            self.assertEqual(saved.read_bytes(), before)
            # Exercise the embedded UI directly, so the test owns its only server.
            with subprocess.Popen([str(core), 'ui-serve'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE) as ui:
                try:
                    endpoint_file = root / 'data/workspace/endpoint.json'
                    for _ in range(100):
                        if endpoint_file.exists(): break
                        if ui.poll() is not None: self.fail(ui.stderr.read().decode())
                        time.sleep(.02)
                    endpoint = json.loads(endpoint_file.read_text())
                    for url, content in [('/', b'Brote'), ('/app.js', b'task'), ('/style.css', b'body'), ('/brote-plant.png', b'\x89PNG\r\n\x1a\n')]:
                        with urllib.request.urlopen(endpoint + url, timeout=5) as response:
                            self.assertIn(content, response.read())
                            if url.endswith('.png'): self.assertEqual(response.headers['Content-Type'], 'image/png')
                finally:
                    ui.terminate()
                    ui.wait(timeout=10)
            saved.unlink()
            for component in ['codex', 'pi', 'vscode', 'core']:
                run(core, 'uninstall', '--component', component)
            self.assertIn('--uninstall-extension ' + manifest['vscode'], calls.read_text())
            self.assertFalse((root / 'install/current').exists())


if __name__ == '__main__': unittest.main()
