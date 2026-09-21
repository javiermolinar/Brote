"""Real Pi Git install/update against a disposable Git and release mirror.

Opt in with AGENTDEBUGGER_RELEASE_DIR and AGENTDEBUGGER_TEST_PI. Requires Go to
build a second test-only core version, not for the installation being exercised.
"""
import functools
import hashlib
import http.server
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import threading
import time
import unittest
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
SOURCE = 'git:github.com/javiermolinar/Brote'


@unittest.skipUnless(os.environ.get('AGENTDEBUGGER_RELEASE_DIR') and os.environ.get('AGENTDEBUGGER_TEST_PI'),
                     'set release directory and AGENTDEBUGGER_TEST_PI')
class PiGitInstallTest(unittest.TestCase):
    def test_install_update_and_remove_preserve_running_core(self):
        self.check_git_flow(ignore_scripts=False)

    def test_first_load_prepares_core_when_npm_scripts_are_disabled(self):
        self.check_git_flow(ignore_scripts=True)

    def check_git_flow(self, ignore_scripts):
        release = Path(os.environ['AGENTDEBUGGER_RELEASE_DIR']).resolve()
        manifest = json.loads((release / 'release-manifest.json').read_text())
        version = manifest['version']
        newer = version + '-pi-update-test'
        system = 'darwin' if platform.system() == 'Darwin' else 'linux'
        arch = 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'
        target = system + '-' + ('x64' if arch == 'amd64' else arch)
        pi = os.environ['AGENTDEBUGGER_TEST_PI']
        with tempfile.TemporaryDirectory(prefix='brote pi git ') as directory:
            root = Path(directory)
            source, mirrors, project = root / 'source', root / 'releases', root / 'project'
            for path in [source, mirrors, project, root / 'home', root / 'pi-agent']:
                path.mkdir()
            # Snapshot current source without copying credentials, local targets or caches.
            names = subprocess.check_output(['git', 'ls-files', '-co', '--exclude-standard', '-z'], cwd=ROOT).decode().split('\0')
            for name in set(names):
                file = ROOT / name
                if name and file.is_file():
                    dest = source / name
                    dest.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(file, dest)
            env = {k: v for k, v in os.environ.items() if k in ('PATH', 'TMPDIR', 'LANG', 'LC_ALL', 'SHELL')}
            env.update(HOME=str(root / 'home'), PI_CODING_AGENT_DIR=str(root / 'pi-agent'),
                       PI_TELEMETRY='0', CODEX_THREAD_ID='',
                       npm_config_ignore_scripts='true' if ignore_scripts else 'false',
                       DEBUG_HANDOVER_HOME=str(root / 'sessions'), AGENTDEBUGGER_DATA_DIR=str(root / 'data'),
                       BROTE_RUNTIME_CACHE=str(root / 'runtime-cache'), GIT_TERMINAL_PROMPT='0',
                       GIT_CONFIG_COUNT='1', GIT_CONFIG_KEY_0='url.' + source.as_uri() + '.insteadOf',
                       GIT_CONFIG_VALUE_0='https://github.com/javiermolinar/Brote')

            def run(*args, cwd=project, source_text=None):
                result = subprocess.run([str(a) for a in args], cwd=cwd, env=env,
                                        input=source_text, capture_output=True, text=True, timeout=120)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                return result.stdout

            run('git', 'init', '-b', 'main', cwd=source)
            run('git', 'config', 'user.name', 'Brote installation test', cwd=source)
            run('git', 'config', 'user.email', 'brote-test@example.invalid', cwd=source)
            run('git', 'add', '.', cwd=source)
            run('git', 'commit', '-m', 'Initial test package', cwd=source)
            for tag in [version, newer]:
                folder = mirrors / ('v' + tag)
                folder.mkdir()
                binary = folder / f'brote-v{tag}-{system}-{arch}'
                if tag == version:
                    shutil.copy2(release / binary.name, binary)
                else:
                    run('go', 'build', '-ldflags', f'-X agentdebugger/internal/cli.Version={tag}',
                        '-o', binary, './cmd/brote', cwd=ROOT)
                (folder / 'SHA256SUMS').write_text(f'{hashlib.sha256(binary.read_bytes()).hexdigest()}  {binary.name}\n')

            requests = []
            class Handler(http.server.SimpleHTTPRequestHandler):
                def log_message(self, *args):
                    pass

                def do_GET(self):
                    requests.append(self.path)
                    super().do_GET()

            with http.server.ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(Handler, directory=str(mirrors))) as mirror:
                thread = threading.Thread(target=mirror.serve_forever, daemon=True)
                thread.start()
                env['BROTE_RELEASE_BASE_URL'] = f'http://127.0.0.1:{mirror.server_port}/'
                try:
                    run(pi, 'install', SOURCE)
                    settings = json.loads((root / 'pi-agent/settings.json').read_text())
                    self.assertIn(SOURCE, settings['packages'])
                    checkout = root / 'pi-agent/git/github.com/javiermolinar/Brote'
                    link = checkout / 'adapters/pi/runtime' / target / 'brote'
                    self.assertEqual(link.exists(), not ignore_scripts, 'install hook must respect npm script settings')
                    def assert_loaded():
                        output = run(pi, '--mode', 'rpc', '--no-session', '--offline',
                                     source_text='{"type":"get_commands","id":"git-smoke"}\n')
                        replies = [json.loads(line) for line in output.splitlines() if line.startswith('{')]
                        reply = next((r for r in replies if r.get('id') == 'git-smoke'), None)
                        self.assertIsNotNone(reply, output)
                        self.assertTrue(reply['success'], reply)
                        names = {c['name'] for c in reply['data']['commands']}
                        self.assertTrue({'debug-connect', 'debug-sessions', 'debug-stop'}.issubset(names), names)

                    assert_loaded()
                    old = link.resolve(strict=True)
                    self.assertEqual(json.loads(run(old, 'version'))['version'], version)
                    self.assertFalse((checkout / 'node_modules/typescript').exists(), 'Pi should not need build dependencies')
                    self.assertEqual(json.loads(run(checkout / 'scripts/debug-handover', 'version'))['version'], version)
                    # Keep the actual old core serving throughout Git cleanup/update/removal.
                    with subprocess.Popen([str(old), 'ui-serve'], cwd=project, env=env,
                                          stdout=subprocess.DEVNULL, stderr=subprocess.PIPE) as core:
                        try:
                            endpoint_file = root / 'data/workspace/endpoint.json'
                            for _ in range(100):
                                if endpoint_file.exists():
                                    break
                                if core.poll() is not None:
                                    self.fail(core.stderr.read().decode())
                                time.sleep(.05)
                            endpoint = json.loads(endpoint_file.read_text())
                            package = json.loads((source / 'package.json').read_text())
                            package['version'] = newer
                            (source / 'package.json').write_text(json.dumps(package, indent=2) + '\n')
                            run('git', 'add', 'package.json', cwd=source)
                            run('git', 'commit', '-m', 'Update test package version', cwd=source)
                            run(pi, 'update', '--extensions')
                            assert_loaded()
                            current = link.resolve(strict=True)
                            self.assertNotEqual(current, old)
                            self.assertEqual(json.loads(run(current, 'version'))['version'], newer)
                            self.assertEqual(json.loads(run(old, 'version'))['version'], version)
                            # Reinstall at the same revision uses the verified cache offline.
                            count = len(requests)
                            run(pi, 'install', SOURCE)
                            self.assertEqual(len(requests), count)
                            run(pi, 'remove', SOURCE)
                            self.assertNotIn(SOURCE, json.loads((root / 'pi-agent/settings.json').read_text())['packages'])
                            self.assertTrue(old.exists())
                            self.assertTrue(current.exists())
                            self.assertIsNone(core.poll())
                            with urllib.request.urlopen(endpoint, timeout=5) as response:
                                self.assertIn(b'Brote', response.read())
                        finally:
                            core.terminate()
                            core.wait(timeout=10)
                    self.assertIn(f'/v{newer}/brote-v{newer}-{system}-{arch}', requests)
                finally:
                    mirror.shutdown()
                    thread.join(timeout=5)


if __name__ == '__main__':
    unittest.main()
