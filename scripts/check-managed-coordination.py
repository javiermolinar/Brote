#!/usr/bin/env python3
"""Real CLI/Delve coordination check; protocol driver, not a real model host test."""
import json, os, queue, subprocess, tempfile, threading, time
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]

def run():
    with tempfile.TemporaryDirectory(prefix='brote-managed-') as tmp:
        work = Path(tmp)
        env = {k:v for k,v in os.environ.items() if not k.startswith('OTEL_EXPORTER_OTLP')}
        env.update(DEBUG_HANDOVER_HOME=str(work/'sessions'), AGENTDEBUGGER_DATA_DIR=str(work/'data'))
        core, binary = work/'brote', work/'target'
        subprocess.run(['go','build','-o',str(core),'./cmd/brote'],cwd=ROOT,check=True)
        (work/'main.go').write_text('package main\nimport "time"\nfunc main(){for{time.Sleep(time.Second)}}\n')
        subprocess.run(['go','build','-gcflags=all=-N -l','-o',str(binary),str(work/'main.go')],check=True)
        def cli(*args, fail=False):
            r=subprocess.run([str(core),*args],env=env,capture_output=True,text=True,timeout=30)
            if fail:
                assert r.returncode, r.stdout
                return r.stderr
            if r.returncode: raise RuntimeError(r.stderr)
            return json.loads(r.stdout)
        s=cli('start','--service','--binary',str(binary),'--project',str(work),'--thread','','--binding','pi:test','--name','Pi')
        sid=s['id']; host=None
        try:
            host=subprocess.Popen([str(core),'events',sid,'--managed','--consumer','host','--binding','pi:test'],env=env,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            frames=queue.Queue()
            def reader():
                for line in host.stdout: frames.put(json.loads(line))
            threading.Thread(target=reader,daemon=True).start()
            def frame(kind):
                deadline=time.monotonic()+10
                while True:
                    f=frames.get(timeout=max(.01,deadline-time.monotonic()))
                    if f['type']=='error':raise RuntimeError(f)
                    if f['type']==kind:return f
            ready=frame('ready'); challenge=frame('liveness');instance=ready['consumer']['instance']
            def fact(seq,state,proof=''):
                host.stdin.write(json.dumps({'type':'host','fact':dict(instance=instance,sequence=seq,turn='turn',state=state,challenge=proof)})+'\n');host.stdin.flush();frame('host-state')
            fact(1,'active',challenge['challenge'])
            state=cli('state',sid,'--brief')
            task=cli('task-start',sid,'--binding','pi:test','--revision',str(state['binding']['revision']),'--instruction','Validate shared execution')['task']['id']
            cli('task-heartbeat',sid,'--binding','pi:test','--task',task,'--consumer','host','--instance',instance,'--turn','turn')
            cli('continue',sid,'--binding','pi:test','--task',task)
            cli('pause',sid,'--human')
            deadline=time.monotonic()+5
            while cli('state',sid,'--brief')['status']=='running':
                assert time.monotonic()<deadline
                time.sleep(.05)
            fact(2,'active')
            cli('task-heartbeat',sid,'--binding','pi:test','--task',task,fail=True)
            state=cli('state',sid,'--brief');assert state['task']['status']=='cancelled'
            task=cli('task-start',sid,'--binding','pi:test','--revision',str(state['binding']['revision']),'--instruction','Validate host loss')['task']['id']
            cli('task-heartbeat',sid,'--binding','pi:test','--task',task,'--consumer','host','--instance',instance,'--turn','turn')
            cli('continue',sid,'--binding','pi:test','--task',task)
            host.stdin.close();host.wait(timeout=20)
            deadline=time.monotonic()+5
            while True:
                state=cli('state',sid,'--brief')
                if state['status']=='paused' and state['task']['status']=='cancelled':break
                assert time.monotonic()<deadline, state
                time.sleep(.05)
            print(json.dumps({'session':sid,'humanPausePreserved':True,'closedHostPausedTarget':True,'cancelledScopeNotRevived':True}))
        finally:
            if host and host.poll() is None:host.terminate();host.wait(timeout=20)
            cli('end-session',sid,'--confirmed')
if __name__=='__main__':run()
