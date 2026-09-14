"""Isolated native Bee pane lifecycle and live-render acceptance. No user sessions."""
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import time

ROOT=Path(__file__).resolve().parents[2]
R=ROOT/'.local/panel-native'
BASE={k:v for k,v in os.environ.items() if not k.startswith(('HERDR_','BEE_'))}
HERDR=shutil.which('herdr')

def call(sock,method,params=None):
    with socket.socket(socket.AF_UNIX) as conn:
        conn.settimeout(8);conn.connect(str(sock))
        conn.sendall((json.dumps({'id':'panel-test','method':method,'params':params or {}})+'\n').encode())
        result=json.loads(conn.makefile('rb').readline())
        if 'error' in result:raise RuntimeError(result['error'])
        return result['result']

def wait(test,timeout=15):
    until=time.monotonic()+timeout
    while time.monotonic()<until:
        if test():return
        time.sleep(.1)
    raise RuntimeError('panel acceptance timed out')

def main():
    R.mkdir(mode=0o700, parents=True)
    sock=R/'herdr/herdr.sock'
    conf=R/'herdr';conf.mkdir()
    (conf/'config.toml').write_text('onboarding = false\n[terminal]\ndefault_shell = "/bin/sh"\nshell_mode = "non_login"\n[update]\nversion_check = false\nmanifest_check = false\n')
    env=dict(BASE,XDG_CONFIG_HOME=str(R),XDG_STATE_HOME=str(R/'state'),BEE_CONFIG_DIR=str(R/'bee-config'),TERM='xterm-256color')
    plugin=R/'plugin';plugin.mkdir()
    subprocess.run(['go','-C',str(ROOT/'bee'),'build','-o',str(plugin/'bee'),'./cmd/bee'],check=True,timeout=90)
    shutil.copyfile(ROOT/'bee/plugin/herdr-plugin.toml',plugin/'herdr-plugin.toml')
    process=None
    try:
        with (R/'herdr.log').open('w') as log:
            process=subprocess.Popen([HERDR,'server'],env=env,stdin=subprocess.DEVNULL,stdout=log,stderr=log)
            wait(sock.exists)
            ws=call(sock,'workspace.create',{'cwd':str(R),'label':'Bee panel acceptance','focus':True})
            root=ws['root_pane']['pane_id']
            tab=ws['tab']['tab_id']
            workspace=ws['workspace']['workspace_id']
            call(sock,'plugin.link',{'path':str(plugin),'enabled':True})
            panelenv=dict(env,HERDR_ENV='1',HERDR_SOCKET_PATH=str(sock),HERDR_PANE_ID=root,
                          HERDR_PLUGIN_CONTEXT_JSON=json.dumps({'focused_pane_id':root}))
            def action(current):
                panelenv['HERDR_PLUGIN_CONTEXT_JSON']=json.dumps({'focused_pane_id':current})
                p=subprocess.run([str(plugin/'bee'),'open'],env=panelenv,capture_output=True,text=True,timeout=20)
                if p.returncode:raise RuntimeError(p.stderr)
                return json.loads(p.stdout)['panel']
            assert action(root)=='open'
            panes=call(sock,'pane.list',{'workspace_id':workspace})['panes']
            panel=next(p for p in panes if p['pane_id']!=root and p['tab_id']==tab)['pane_id']
            def text():
                result=call(sock,'pane.read',{'pane_id':panel,'format':'text','source':'visible'})
                return json.dumps(result)
            wait(lambda:'[1] Connection' in text() and '[2] Sharing' in text())
            assert 'Join Hive' in text() and 'Invitation' in text()
            assert 'SSH identity' not in text()
            assert action(root)=='focus'
            assert len(call(sock,'pane.list',{'workspace_id':workspace})['panes'])==2
            # External CLI mutation must appear without a manual refresh.
            subprocess.run([str(plugin/'bee'),'name','Panel refresh verified'],env=panelenv,check=True,capture_output=True,timeout=15)
            wait(lambda:'Panel refresh verified' in text())
            # Switch to the session list using actual terminal input.
            call(sock,'pane.send_text',{'pane_id':panel,'text':'2'})
            wait(lambda:'Shared sessions' in text())
            call(sock,'pane.send_text',{'pane_id':panel,'text':'3'})
            wait(lambda:'Bees in this Hive' in text())
            assert 'Set up Connection to view Bees.' in text()
            if os.environ.get('BEE_TEST_RENDER_DIR'):
                output=Path(os.environ['BEE_TEST_RENDER_DIR']);output.mkdir(parents=True,exist_ok=True)
                frame=call(sock,'pane.read',{'pane_id':panel,'format':'ansi','source':'visible'})
                (output/'native-frame.json').write_text(json.dumps(frame))
            assert action(panel)=='close'
            panes=call(sock,'pane.list',{'workspace_id':workspace})['panes']
            assert len(panes)==1 and panes[0]['pane_id']==root
            assert action(root)=='open'
            panes=call(sock,'pane.list',{'workspace_id':workspace})['panes']
            reopened=next(p['pane_id'] for p in panes if p['pane_id']!=root)
            assert reopened!=panel
            call(sock,'pane.send_text',{'pane_id':reopened,'text':'q'})
            wait(lambda:len(call(sock,'pane.list',{'workspace_id':workspace})['panes'])==1)
            print('Native Bee: split/open/focus/close/reopen, live CLI refresh and keyboard navigation passed')
    finally:
        if process:
            try:call(sock,'server.stop')
            except (OSError,RuntimeError):pass
            try:process.wait(timeout=10)
            except subprocess.TimeoutExpired:process.kill();process.wait(timeout=5)
        shutil.rmtree(R)

if __name__=='__main__':main()
