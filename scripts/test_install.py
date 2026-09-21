"""Exercise the curl installer without the network or a real installation."""
import hashlib, json, os, platform, subprocess, sys, tarfile, tempfile, unittest
from pathlib import Path

class BootstrapTest(unittest.TestCase):
    def run_installer(self, corrupt=False, prefix="brote", local=False, canonical=False, embedded=False, piped=False):
        with tempfile.TemporaryDirectory(prefix='brote installer ') as d:
            root=Path(d);stubs=root/'stubs';stubs.mkdir()
            payload=root/'payload'/'delve-llm-adapter'/'bin';payload.mkdir(parents=True)
            executable=payload/'delve-llm-adapter'
            executable.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$INSTALL_ARGS"\nprintf "%s\\n" "$0" > "$INSTALL_BINARY"\n')
            executable.chmod(0o755)
            if canonical:
                brote=payload/'brote'
                brote.write_text(executable.read_text())
                brote.chmod(0o755)
            system='darwin' if sys.platform=='darwin' else 'linux'
            arch='arm64' if platform.machine() in ('arm64','aarch64') else 'amd64'
            asset=f'{prefix}-v0.2.0-{system}-{arch}.tar.gz'
            archive=root/asset
            with tarfile.open(archive,'w:gz') as tar:tar.add(root/'payload'/'delve-llm-adapter',arcname='delve-llm-adapter')
            digest='0'*64 if corrupt else hashlib.sha256(archive.read_bytes()).hexdigest()
            (root/'SHA256SUMS').write_text(f'{digest}  {asset}\n')
            curl=stubs/'curl'
            curl.write_text(f'#!{sys.executable}\nimport os,sys,shutil\nfrom pathlib import Path\nPath(os.environ["CURL_CALLED"]).touch()\na=sys.argv[1:];url=a[a.index("-o")-1];out=a[a.index("-o")+1];shutil.copyfile(Path(os.environ["FIXTURE"])/url.rsplit("/",1)[-1],out)\n')
            curl.chmod(0o755)
            args=root/'args'
            binary=root/'binary'
            curl_called=root/'curl-called'
            source=['--from',d] if local else ([] if embedded else ['--repository','test/repo','--version','v0.2.0'])
            installer=Path(__file__).resolve().parent.parent/'install.sh'
            if embedded:
                packaged=root/'install.sh'
                packaged.write_text(installer.read_text().replace('@REPOSITORY@','test/repo'))
                installer=packaged
            command=['sh','-s','--'] if piped else ['sh',str(installer)]
            result=subprocess.run([*command,*source,'--agent','pi','--editor','vscode'],input=installer.read_text() if piped else None,cwd=root,env={**os.environ,'PATH':str(stubs)+os.pathsep+os.environ['PATH'],'BROTE_REPOSITORY':'','DELVE_LLM_ADAPTER_REPOSITORY':'','FIXTURE':d,'INSTALL_ARGS':str(args),'INSTALL_BINARY':str(binary),'CURL_CALLED':str(curl_called)},capture_output=True,text=True)
            self.assertEqual(curl_called.exists(),not local)
            if corrupt:
                self.assertNotEqual(result.returncode,0);self.assertIn('checksum mismatch',result.stderr);self.assertFalse(args.exists())
            else:
                self.assertEqual(result.returncode,0,result.stderr);values=args.read_text().splitlines();self.assertEqual(values[0],'setup');self.assertEqual(values[-4:],['--agent','pi','--editor','vscode'])
                self.assertEqual(Path(binary.read_text().strip()).name,'brote' if canonical else 'delve-llm-adapter')
                self.assertFalse(Path(values[2]).exists(), 'temporary bundle should be cleaned up')
                self.assertIn('/reload',result.stdout)
    def test_verified_bundle_and_argument_forwarding(self):self.run_installer()
    def test_packaged_installer_uses_embedded_repository(self):self.run_installer(embedded=True,canonical=True)
    def test_piped_release_installer_needs_no_checkout(self):self.run_installer(embedded=True,canonical=True,piped=True)
    def test_legacy_release_still_installs(self):self.run_installer(prefix="delve-llm-adapter")
    def test_corruption_never_executes(self):self.run_installer(True)
    def test_local_release_without_network_or_extraction_step(self):self.run_installer(local=True,canonical=True)
    def test_local_corruption_never_executes(self):self.run_installer(corrupt=True,local=True)
    def test_help_needs_no_repository(self):
        result=subprocess.run(['sh','install.sh','--help'],cwd=Path(__file__).resolve().parent.parent,capture_output=True,text=True)
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertIn('--from DIR',result.stdout)
    def test_local_release_requires_checksums(self):
        with tempfile.TemporaryDirectory() as d:
            result=subprocess.run(['sh','install.sh','--from',d],cwd=Path(__file__).resolve().parent.parent,capture_output=True,text=True)
            self.assertNotEqual(result.returncode,0)
            self.assertIn('must contain SHA256SUMS',result.stderr)
if __name__=='__main__':unittest.main()
