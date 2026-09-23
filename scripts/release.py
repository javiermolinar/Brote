#!/usr/bin/env python3
"""Build core bundles, a self-contained Pi package, Codex plugin and native VSIXs.

Builds artifacts only. Registry publication is a separate, explicit operation.
"""
import argparse
import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import tarfile
import tempfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TARGETS = {'darwin/arm64': 'darwin-arm64', 'darwin/amd64': 'darwin-x64',
           'linux/arm64': 'linux-arm64', 'linux/amd64': 'linux-x64'}


def run(*args, cwd=ROOT, env=None):
    subprocess.run(args, cwd=cwd, env=env, check=True)


def manifest(file):
    return json.loads(file.read_text())


def write_json(file, value):
    file.parent.mkdir(parents=True, exist_ok=True)
    file.write_text(json.dumps(value, indent=2) + '\n')


def copy_runtime(source, destination):
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, destination)
    destination.chmod(0o755)


def copy_brand(destination, include_screenshot=False):
    destination.mkdir(parents=True, exist_ok=True)
    shutil.copy2(ROOT / 'assets/brote-plant.png', destination / 'brote-plant.png')
    if include_screenshot:
        shutil.copy2(ROOT / 'assets/debugger-conversation.png', destination / 'debugger-conversation.png')


def copy_integration_readme(source, destination):
    destination.write_text(source.read_text().replace('../../assets/', 'assets/'))


def archive_tree(source, output):
    with tarfile.open(output, 'w:gz') as archive:
        archive.add(source, arcname=source.name)


def package_release(args):
    version = args.version or manifest(ROOT / 'package.json')['version']
    version = version.removeprefix('v')
    if not re.fullmatch(r'\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?', version):
        raise ValueError('version must be SemVer, e.g. 0.4.0 or 0.4.0-preview.1')
    if args.repository and not re.fullmatch(r'[\w.-]+/[\w.-]+', args.repository):
        raise ValueError('repository must be OWNER/REPO')
    if args.publisher and not re.fullmatch(r'[\w-]+', args.publisher):
        raise ValueError('invalid Marketplace publisher')
    if not re.fullmatch(r'(?:@[a-z0-9_-]+/)?[a-z0-9][a-z0-9._-]*', args.npm_name):
        raise ValueError('invalid npm package name')
    host = ('darwin' if platform.system() == 'Darwin' else 'linux') + '/' + ('arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64')
    targets = args.platforms or ([host] if args.host_only else list(TARGETS))
    if any(t not in TARGETS for t in targets):
        raise ValueError('supported targets: ' + ', '.join(TARGETS))
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    # Rebuild before packaging; artifacts never silently reuse stale bundles.
    run('npm', 'run', 'build')
    artifacts = []
    with tempfile.TemporaryDirectory(prefix='brote-release-') as tmp:
        work = Path(tmp)
        runtimes = {}
        for target in targets:
            system, arch = target.split('/')
            binary = work / TARGETS[target] / 'brote'
            binary.parent.mkdir()
            run('go', 'build', '-trimpath', '-ldflags', f'-X agentdebugger/internal/cli.Version={version}', '-o', str(binary), './cmd/brote', env={**os.environ, 'GOOS': system, 'GOARCH': arch, 'CGO_ENABLED': '0'})
            runtimes[target] = binary
            # Pi Git installs fetch only the matching native core; its WebUI is embedded.
            native = out / f'brote-v{version}-{system}-{arch}'
            copy_runtime(binary, native)
            artifacts.append(native)

        pi = work / 'pi'
        pi.mkdir()
        pi_manifest = manifest(ROOT / 'adapters/pi/package.json')
        pi_manifest.update(name=args.npm_name, version=version)
        pi_manifest['pi']['extensions'] = ['./index.js']
        if args.repository:
            pi_manifest['repository'] = {'type': 'git', 'url': 'https://github.com/' + args.repository + '.git', 'directory': 'adapters/pi'}
        write_json(pi / 'package.json', pi_manifest)
        run(str(ROOT / 'node_modules/.bin/esbuild'), 'adapters/pi/index.ts', '--bundle', '--platform=node', '--format=esm', '--target=node22', '--external:typebox', '--outfile=' + str(pi / 'index.js'))
        for target, binary in runtimes.items():
            copy_runtime(binary, pi / 'runtime' / TARGETS[target] / 'brote')
        shutil.copytree(ROOT / 'skills', pi / 'skills')
        copy_integration_readme(ROOT / 'adapters/pi/README.md', pi / 'README.md')
        copy_brand(pi / 'assets')
        shutil.copy2(ROOT / 'LICENSE', pi / 'LICENSE')
        # npm pack applies the actual publication file allowlist, without scripts.
        run('npm', 'pack', '--ignore-scripts', '--pack-destination', str(out), cwd=pi)
        npm_tar = out / (args.npm_name.replace('@', '').replace('/', '-') + '-' + version + '.tgz')
        artifacts.append(npm_tar)

        plugin = work / 'brote'
        copy_brand(plugin / 'assets')
        shutil.copytree(ROOT / 'skills', plugin / 'skills')
        shutil.copytree(ROOT / 'docs', plugin / 'docs')
        plugin_manifest = manifest(ROOT / '.codex-plugin/plugin.json')
        plugin_manifest['version'] = version
        write_json(plugin / '.codex-plugin/plugin.json', plugin_manifest)
        copy_runtime(ROOT / 'scripts/runtime-launcher.sh', plugin / 'scripts/debug-handover')
        for target, binary in runtimes.items():
            copy_runtime(binary, plugin / 'runtime' / TARGETS[target] / 'brote')
        shutil.copy2(ROOT / 'LICENSE', plugin / 'LICENSE')
        plugin_zip = out / f'brote-codex-{version}.zip'
        with zipfile.ZipFile(plugin_zip, 'w', zipfile.ZIP_DEFLATED) as archive:
            for file in sorted(plugin.rglob('*')):
                if file.is_file(): archive.write(file, Path(plugin.name) / file.relative_to(plugin))
        artifacts.append(plugin_zip)

        extension_manifest = manifest(ROOT / 'packages/vscode/package.json')
        extension_manifest['version'] = version
        if args.publisher: extension_manifest['publisher'] = args.publisher
        if args.repository:
            extension_manifest['repository'] = {'type': 'git', 'url': 'https://github.com/' + args.repository + '.git'}
        for target, binary in runtimes.items():
            ext = work / ('vscode-' + TARGETS[target])
            shutil.copytree(ROOT / 'packages/vscode/dist', ext / 'dist')
            copy_runtime(binary, ext / 'runtime/brote')
            copy_brand(ext / 'assets')
            shutil.copy2(ROOT / 'packages/vscode/assets/tracepoint.svg', ext / 'assets/tracepoint.svg')
            copy_integration_readme(ROOT / 'packages/vscode/README.md', ext / 'README.md')
            shutil.copy2(ROOT / 'LICENSE', ext / 'LICENSE')
            shutil.copy2(ROOT / 'THIRD_PARTY_NOTICES.md', ext / 'THIRD_PARTY_NOTICES.md')
            (ext / '.vscodeignore').write_text('')
            write_json(ext / 'package.json', extension_manifest)
            vsix = out / f'brote-{version}-{TARGETS[target]}.vsix'
            command = ['npm', 'exec', '--yes', '--package=@vscode/vsce@4.0.0', '--', 'vsce', 'package', '--no-dependencies', '--target', TARGETS[target], '--out', str(vsix)]
            if args.repository:
                command += ['--baseImagesUrl', f'https://raw.githubusercontent.com/{args.repository}/v{version}/']
            else:
                command += ['--allow-missing-repository', '--no-rewrite-relative-links']
            run(*command, cwd=ext)
            artifacts.append(vsix)
            system, arch = target.split('/')
            # Preserve the old archive root and executable aliases for upgrades.
            bundle = work / TARGETS[target] / 'delve-llm-adapter'
            copy_runtime(binary, bundle / 'bin/brote')
            copy_runtime(binary, bundle / 'bin/delve-llm-adapter')
            copy_runtime(binary, bundle / 'bin/agentdebugger')
            copy_runtime(binary, bundle / 'bin/debug-handover')
            for name in ['README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md']:
                shutil.copy2(ROOT / name, bundle / name)
            shutil.copytree(ROOT / 'docs', bundle / 'docs')
            copy_brand(bundle / 'assets', include_screenshot=True)
            shutil.copytree(pi, bundle / 'adapters/pi')
            shutil.copytree(plugin, bundle / 'adapters/codex/brote')
            (bundle / 'editors').mkdir()
            shutil.copy2(vsix, bundle / 'editors/brote.vsix')
            write_json(bundle / 'release.json', {'version': version, 'os': system, 'arch': arch, 'protocol': 2, 'VSCodeExtension': extension_manifest['publisher'] + '.' + extension_manifest['name']})
            dest = out / f'brote-v{version}-{system}-{arch}.tar.gz'
            archive_tree(bundle, dest)
            artifacts.append(dest)
        installer = out / 'install.sh'
        installer.write_text((ROOT / 'install.sh').read_text().replace('@REPOSITORY@', args.repository or '@REPOSITORY@'))
        installer.chmod(0o755)
        artifacts.append(installer)
    write_json(out / 'release-manifest.json', {'version': version, 'protocol': 2, 'platforms': targets, 'npm': args.npm_name, 'vscode': extension_manifest['publisher'] + '.' + extension_manifest['name'], 'repository': args.repository, 'codex': 'brote', 'artifacts': [p.name for p in artifacts]})
    artifacts.append(out / 'release-manifest.json')
    (out / 'SHA256SUMS').write_text(''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n' for p in artifacts))
    print(f'Built {len(artifacts)} artifacts in {out}; nothing published.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version')
    parser.add_argument('--platform', action='append', dest='platforms', choices=list(TARGETS))
    parser.add_argument('--host-only', action='store_true')
    parser.add_argument('--output', type=Path, default=Path('dist/releases'))
    parser.add_argument('--repository', default=os.environ.get('GITHUB_REPOSITORY', 'javiermolinar/Brote'))
    parser.add_argument('--publisher', default=os.environ.get('VSCODE_PUBLISHER', ''))
    parser.add_argument('--npm-name', default=os.environ.get('BROTE_NPM_NAME') or os.environ.get('AGENTDEBUGGER_NPM_NAME', 'brote-pi'))
    args = parser.parse_args()
    try: package_release(args)
    except ValueError as error: parser.error(str(error))


if __name__ == '__main__': main()
