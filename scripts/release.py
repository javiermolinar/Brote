#!/usr/bin/env python3
"""Build standalone platform bundles; no end-user Go/Node dependency."""
import argparse, hashlib, json, os, platform, shutil, subprocess, tarfile, tempfile
from pathlib import Path
p=argparse.ArgumentParser()
p.add_argument('--version',required=True)
p.add_argument('--platform',action='append',dest='platforms',help='darwin/arm64, linux/amd64, ...')
p.add_argument('--output',type=Path,default=Path('dist/releases'))
p.add_argument('--repository',default=os.environ.get('GITHUB_REPOSITORY',''))
a=p.parse_args()
import re
if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._+-]*',a.version):p.error('invalid version')
r=Path(__file__).resolve().parent.parent
out=a.output.resolve();out.mkdir(parents=True,exist_ok=True)
vsix=r/'editors/vscode/debug-handover-0.1.0.vsix'
if not vsix.is_file():p.error('run npm run package:vscode first')
checks=[]
for target in a.platforms or ['darwin/arm64','darwin/amd64','linux/arm64','linux/amd64']:
    if target not in ['darwin/arm64','darwin/amd64','linux/arm64','linux/amd64']:p.error('unsupported platform')
    system,arch=target.split('/')
    with tempfile.TemporaryDirectory() as tmp:
        bundle=Path(tmp)/'delve-llm-adapter';(bundle/'bin').mkdir(parents=True)
        subprocess.run(['go','build','-trimpath','-ldflags',f'-X debug-handover/internal/cli.Version={a.version}','-o',str(bundle/'bin/delve-llm-adapter'),'./cmd/debug-handover'],cwd=r,env={**os.environ,'GOOS':system,'GOARCH':arch,'CGO_ENABLED':'0'},check=True)
        for name in ['README.md','LICENSE','THIRD_PARTY_NOTICES.md']:shutil.copy2(r/name,bundle/name)
        shutil.copytree(r/'docs',bundle/'docs')
        shutil.copytree(r/'adapters/pi',bundle/'adapters/pi')
        shutil.copytree(r/'skills',bundle/'adapters/pi/skills')
        plugin=bundle/'adapters/codex/debug-handover'
        shutil.copytree(r/'skills',plugin/'skills')
        shutil.copytree(r/'docs',plugin/'docs')
        (plugin/'.codex-plugin').mkdir()
        manifest=json.loads((r/'.codex-plugin/plugin.json').read_text());manifest['version']=a.version.removeprefix('v')
        (plugin/'.codex-plugin/plugin.json').write_text(json.dumps(manifest,indent=2)+'\n')
        # Codex caches plugins, so include the same prebuilt CLI in that package.
        (plugin/'scripts').mkdir();shutil.copy2(bundle/'bin/delve-llm-adapter',plugin/'scripts/debug-handover')
        (bundle/'editors').mkdir();shutil.copy2(vsix,bundle/'editors/debug-handover.vsix')
        (bundle/'release.json').write_text(json.dumps({'version':a.version,'os':system,'arch':arch,'protocol':2})+'\n')
        dest=out/f'delve-llm-adapter-{a.version}-{system}-{arch}.tar.gz'
        with tarfile.open(dest,'w:gz') as archive:archive.add(bundle,arcname='delve-llm-adapter')
        checks.append(f'{hashlib.sha256(dest.read_bytes()).hexdigest()}  {dest.name}')
        print(dest)
installer=(r/'install.sh').read_text().replace('@REPOSITORY@',a.repository or '@REPOSITORY@')
(out/'install.sh').write_text(installer);os.chmod(out/'install.sh',0o755)
checks.append(f'{hashlib.sha256((out/"install.sh").read_bytes()).hexdigest()}  install.sh')
(out/'SHA256SUMS').write_text('\n'.join(checks)+'\n')
