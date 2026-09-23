"""Opt-in installation checks against real Codex, Pi and VS Code CLIs.

Set AGENTDEBUGGER_RELEASE_DIR and AGENTDEBUGGER_TEST_CODEX, _PI or _CODE.
Each test pipes the packaged installer into sh from an empty project, uses
fresh host profiles, and removes its installation. No model calls are made.
"""
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import time
import unittest
import urllib.request


RELEASE = os.environ.get('AGENTDEBUGGER_RELEASE_DIR')


class HostInstallTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='brote host install ')
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.release = Path(RELEASE).resolve()
        self.manifest = json.loads((self.release / 'release-manifest.json').read_text())
        for name in ['home', 'codex', 'project', 'commands']:
            (self.root / name).mkdir()
        # These are child-process profiles, never the user's existing profiles.
        self.env = {key: value for key, value in os.environ.items()
                    if key in ('PATH', 'TMPDIR', 'LANG', 'LC_ALL', 'SHELL', 'SYSTEMROOT')}
        self.env.update(
            HOME=str(self.root / 'home'), CODEX_HOME=str(self.root / 'codex'),
            PI_CODING_AGENT_DIR=str(self.root / 'pi-agent'), PI_OFFLINE='1', PI_TELEMETRY='0',
            DELVE_LLM_ADAPTER_HOME=str(self.root / 'install'),
            DEBUG_HANDOVER_HOME=str(self.root / 'sessions'),
            AGENTDEBUGGER_DATA_DIR=str(self.root / 'data'), CODEX_THREAD_ID='',
            PATH=str(self.root / 'commands') + os.pathsep + os.environ['PATH'],
        )
        self.core = self.root / 'install/bin/brote'

    def command(self, *args, source=None):
        result = subprocess.run([str(a) for a in args], input=source,
                                cwd=self.root / 'project', env=self.env,
                                capture_output=True, text=True, timeout=60)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result.stdout

    def forward(self, name, executable, *profile):
        # Forward to the real host; the wrapper only supplies isolated profiles.
        wrapper = self.root / 'commands' / name
        wrapper.write_text('#!/bin/sh\nexec ' + shlex.join([executable, *profile]) + ' "$@"\n')
        wrapper.chmod(0o755)

    def install(self, component):
        option = '--editor' if component == 'vscode' else '--agent'
        self.command('sh', '-s', '--', '--from', self.release, option, component,
                     source=(self.release / 'install.sh').read_text())
        state = json.loads(self.command(self.core, 'installation'))['installation']
        self.assertEqual(state['components'], {'core': 'installed', component: 'installed'})
        self.check_runtime(self.core)

    def check_runtime(self, binary):
        info = json.loads(self.command(binary, 'version'))
        self.assertEqual(info['version'], self.manifest['version'])
        self.assertIn('embeddedWebUI', info['capabilities'])
        self.assertIn('taskExecute', info['capabilities'])

    def check_web_ui(self):
        with subprocess.Popen([str(self.core), 'ui-serve'], cwd=self.root / 'project',
                              env=self.env, stdout=subprocess.DEVNULL,
                              stderr=subprocess.PIPE) as server:
            try:
                endpoint = self.root / 'data/workspace/endpoint.json'
                for _ in range(100):
                    if endpoint.exists():
                        break
                    if server.poll() is not None:
                        self.fail(server.stderr.read().decode())
                    time.sleep(.05)
                address = json.loads(endpoint.read_text())
                for route, marker in [('/', b'Brote'), ('/app.js', b'task'),
                                      ('/brote-plant.png', b'\x89PNG\r\n\x1a\n')]:
                    with urllib.request.urlopen(address + route, timeout=5) as response:
                        self.assertIn(marker, response.read())
            finally:
                server.terminate()
                server.wait(timeout=10)

    def uninstall(self, component):
        self.command(self.core, 'uninstall', '--component', component)
        state = json.loads(self.command(self.core, 'installation'))['installation']
        self.assertEqual(state['components'][component], 'removed')
        self.command(self.core, 'uninstall', '--component', 'core')
        self.assertFalse(self.core.exists())

    @unittest.skipUnless(RELEASE and os.environ.get('AGENTDEBUGGER_TEST_CODEX'),
                         'set release directory and AGENTDEBUGGER_TEST_CODEX')
    def test_codex_shell_install(self):
        self.forward('codex', os.environ['AGENTDEBUGGER_TEST_CODEX'])
        self.install('codex')
        self.command(self.core, 'repair')
        installed = json.loads(self.command('codex', 'plugin', 'list', '--json',
                                           '--marketplace', 'brote'))['installed']
        self.assertEqual([p['pluginId'] for p in installed], ['brote@brote'])
        self.assertTrue(installed[0]['enabled'])
        cached = list((self.root / 'codex/plugins/cache').rglob('scripts/debug-handover'))
        self.assertEqual(len(cached), 1, cached)
        self.check_runtime(cached[0])
        self.assertTrue((cached[0].parent.parent / 'skills/debug-handover/SKILL.md').is_file())
        self.check_web_ui()
        self.uninstall('codex')
        installed = json.loads(self.command('codex', 'plugin', 'list', '--json'))['installed']
        self.assertNotIn('brote@brote', [p['pluginId'] for p in installed])

    @unittest.skipUnless(RELEASE and os.environ.get('AGENTDEBUGGER_TEST_PI'),
                         'set release directory and AGENTDEBUGGER_TEST_PI')
    def test_pi_shell_install(self):
        self.forward('pi', os.environ['AGENTDEBUGGER_TEST_PI'])
        self.install('pi')
        self.command(self.core, 'repair')
        package = self.root / 'install/current/adapters/pi'
        self.assertIn(str(package), self.command('pi', 'list'))
        output = self.command('pi', '--mode', 'rpc', '--no-session', '--offline',
                              source='{"type":"get_commands","id":"install-smoke"}\n')
        responses = [json.loads(line) for line in output.splitlines() if line.startswith('{')]
        response = next((item for item in responses if item.get('id') == 'install-smoke'), None)
        self.assertIsNotNone(response, output)
        self.assertTrue(response['success'], response)
        commands = {item['name'] for item in response['data']['commands']}
        self.assertTrue({'debug-connect', 'debug-sessions', 'debug-stop'}.issubset(commands), commands)
        self.assertTrue((package / 'skills/debug-handover/SKILL.md').is_file())
        self.check_web_ui()
        self.uninstall('pi')
        self.assertNotIn(str(package), self.command('pi', 'list'))

    @unittest.skipUnless(RELEASE and os.environ.get('AGENTDEBUGGER_TEST_CODE'),
                         'set release directory and AGENTDEBUGGER_TEST_CODE')
    def test_vscode_shell_install(self):
        extensions = self.root / 'code-extensions'
        self.forward('code', os.environ['AGENTDEBUGGER_TEST_CODE'],
                     '--user-data-dir', str(self.root / 'code-user'),
                     '--extensions-dir', str(extensions))
        self.install('vscode')
        self.command(self.core, 'repair')
        identity = self.manifest['vscode']
        self.assertIn(identity + '@' + self.manifest['version'],
                      self.command('code', '--list-extensions', '--show-versions'))
        installed = list(extensions.glob(identity + '-*/package.json'))
        self.assertEqual(len(installed), 1, installed)
        metadata = json.loads(installed[0].read_text())
        self.assertEqual(metadata['displayName'], 'Brote')
        self.assertTrue((installed[0].parent / metadata['main']).is_file())
        self.assertFalse((installed[0].parent / 'bin').exists())
        self.assertEqual([item['type'] for item in metadata.get('contributes', {}).get('debuggers', [])], ['brote'])
        self.check_web_ui()
        self.uninstall('vscode')
        self.assertNotIn(identity, self.command('code', '--list-extensions'))


if __name__ == '__main__':
    unittest.main()
