#!/usr/bin/env python3
"""Package distributable source and built UI; exclude targets, state, and secrets."""
import argparse
import json
from pathlib import Path
import zipfile

parser = argparse.ArgumentParser()
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parent.parent
plugin_name = json.loads((root / '.codex-plugin' / 'plugin.json').read_text())['name']
files = [root / p for p in (
    'README.md', 'ARCHITECTURE.md', 'THIRD_PARTY_NOTICES.md', 'go.mod', '.gitignore',
    '.codex-plugin/plugin.json', 'examples/demo/main.go',
    'package.json', 'package-lock.json', 'tsconfig.json',
)]
files += list(root.glob('*.go'))
for directory in ('web', 'scripts', 'skills', '.github', 'vscode'):
    files += [p for p in (root / directory).rglob('*')
              if p.is_file() and '__pycache__' not in p.parts and p.suffix not in ('.pyc', '.vsix') and 'node_modules' not in p.parts]
files.append(root / 'vscode' / 'debug-handover-0.1.0.vsix')
args.output.parent.mkdir(parents=True, exist_ok=True)
with zipfile.ZipFile(args.output, 'w', zipfile.ZIP_DEFLATED) as archive:
    for path in sorted(files):
        archive.write(path, Path(plugin_name) / path.relative_to(root))
with zipfile.ZipFile(args.output) as archive:
    assert archive.testzip() is None
print(f'{args.output}: {len(files)} files, {args.output.stat().st_size} bytes')
