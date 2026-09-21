#!/usr/bin/env python3
"""Package buildable monorepo source and UI; exclude runtime state and dependencies."""
import argparse
import json
from pathlib import Path
import subprocess
import zipfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--include-vsix', action='store_true')
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    # The Git inventory cannot pick up local conversation exports, targets or state.
    # Include new source files during development while respecting .gitignore.
    names = subprocess.check_output(['git', 'ls-files', '-co', '--exclude-standard', '-z'], cwd=root).decode().split('\0')
    roots = {'assets', 'cmd', 'internal', 'adapters', 'packages', 'docs', 'scripts', 'skills', '.github', '.codex-plugin'}
    top = {'README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'install.sh', 'go.mod', 'go.sum', '.gitignore', 'package.json', 'package-lock.json', 'examples/demo/main.go'}
    files = sorted({root / n for n in names if n and (n in top or Path(n).parts[0] in roots) and (root / n).is_file()})
    if args.include_vsix:
        version = json.loads((root / 'package.json').read_text())['version']
        vsixs = list((root / 'dist/releases').glob(f'brote-{version}-*.vsix'))
        if not vsixs:
            parser.error('VSIX missing; run npm run package:vscode first')
        files += sorted(vsixs)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(args.output, 'w', zipfile.ZIP_DEFLATED) as archive:
        for file in files:
            archive.write(file, Path('brote') / file.relative_to(root))
    print(f'{args.output}: {len(files)} files')


if __name__ == '__main__': main()
