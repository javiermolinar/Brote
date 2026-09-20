#!/usr/bin/env python3
"""Package distributable source and built UI; exclude targets, state, and secrets."""
import argparse
import json
from pathlib import Path
import zipfile

parser = argparse.ArgumentParser()
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--include-vsix', action='store_true', help='Bundle the separately built VS Code extension')
args = parser.parse_args()
root = Path(__file__).resolve().parent.parent
plugin_name = json.loads((root / '.codex-plugin' / 'plugin.json').read_text())['name']
files = [root / p for p in (
    'README.md', 'LICENSE', 'install.sh', 'THIRD_PARTY_NOTICES.md', 'go.mod', '.gitignore',
    '.codex-plugin/plugin.json', 'examples/demo/main.go',
    'package.json', 'package-lock.json',
)]
for directory in ('cmd', 'internal', 'adapters', 'ui', 'editors', 'docs', 'scripts', 'skills', '.github'):
    files += [p for p in (root / directory).rglob('*')
              if p.is_file() and '__pycache__' not in p.parts and p.suffix not in ('.pyc', '.vsix') and 'node_modules' not in p.parts]
if args.include_vsix:
    vsix = root / 'editors' / 'vscode' / 'debug-handover-0.1.0.vsix'
    if not vsix.is_file():
        parser.error('VSIX missing; run npm run package:vscode first, or omit --include-vsix')
    files.append(vsix)
args.output.parent.mkdir(parents=True, exist_ok=True)
with zipfile.ZipFile(args.output, 'w', zipfile.ZIP_DEFLATED) as archive:
    for path in sorted(files):
        archive.write(path, Path(plugin_name) / path.relative_to(root))
with zipfile.ZipFile(args.output) as archive:
    assert archive.testzip() is None
print(f'{args.output}: {len(files)} files, {args.output.stat().st_size} bytes')
