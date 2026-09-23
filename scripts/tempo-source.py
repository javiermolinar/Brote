#!/usr/bin/env python3
"""Archive the exact Brote and unmodified Tempo source used by local release builds."""
from pathlib import Path
import subprocess
import sys
import tarfile
ROOT=Path(__file__).resolve().parent.parent
output=Path(sys.argv[1]).resolve()
output.parent.mkdir(parents=True,exist_ok=True)
tempo=Path(subprocess.check_output(['go','list','-m','-f','{{.Dir}}','github.com/grafana/tempo/v3'],cwd=ROOT,text=True).strip())
files=subprocess.check_output(['git','ls-files','-z','--cached','--others','--exclude-standard'],cwd=ROOT).decode().split('\0')
with tarfile.open(output,'w:gz') as archive:
    for name in sorted(set(files)):
        if not name or not (ROOT/name).is_file():continue
        archive.add(ROOT/name,arcname='brote-source/'+name,recursive=False)
    archive.add(tempo,arcname='brote-source/third_party/tempo')
print(output)
