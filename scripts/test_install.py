"""Exercise the curl installer without the network or a real installation."""
import hashlib, json, os, platform, subprocess, sys, tarfile, tempfile, unittest
from pathlib import Path

class BootstrapTest(unittest.TestCase):
    def run_installer(self, corrupt=False):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);stubs=root/'stubs';stubs.mkdir()
            payload=root/'payload'/'delve-llm-adapter'/'bin';payload.mkdir(parents=True)
            executable=payload/'delve-llm-adapter'
            executable.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$INSTALL_ARGS"\n')
            executable.chmod(0o755)
            system='darwin' if sys.platform=='darwin' else 'linux'
            arch='arm64' if platform.machine() in ('arm64','aarch64') else 'amd64'
            asset=f'delve-llm-adapter-v0.2.0-{system}-{arch}.tar.gz'
            archive=root/asset
            with tarfile.open(archive,'w:gz') as tar:tar.add(root/'payload'/'delve-llm-adapter',arcname='delve-llm-adapter')
            digest='0'*64 if corrupt else hashlib.sha256(archive.read_bytes()).hexdigest()
            (root/'SHA256SUMS').write_text(f'{digest}  {asset}\n')
            curl=stubs/'curl'
            curl.write_text(f'#!{sys.executable}\nimport os,sys,shutil\nfrom pathlib import Path\na=sys.argv[1:];url=a[a.index("-o")-1];out=a[a.index("-o")+1];shutil.copyfile(Path(os.environ["FIXTURE"])/url.rsplit("/",1)[-1],out)\n')
            curl.chmod(0o755)
            args=root/'args'
            result=subprocess.run(['sh','install.sh','--repository','test/repo','--version','v0.2.0','--agent','pi','--editor','vscode'],cwd=Path(__file__).resolve().parent.parent,env={**os.environ,'PATH':str(stubs)+os.pathsep+os.environ['PATH'],'FIXTURE':d,'INSTALL_ARGS':str(args)},capture_output=True,text=True)
            if corrupt:
                self.assertNotEqual(result.returncode,0);self.assertIn('checksum mismatch',result.stderr);self.assertFalse(args.exists())
            else:
                self.assertEqual(result.returncode,0,result.stderr);values=args.read_text().splitlines();self.assertEqual(values[0],'setup');self.assertEqual(values[-4:],['--agent','pi','--editor','vscode'])
    def test_verified_bundle_and_argument_forwarding(self):self.run_installer()
    def test_corruption_never_executes(self):self.run_installer(True)
if __name__=='__main__':unittest.main()
