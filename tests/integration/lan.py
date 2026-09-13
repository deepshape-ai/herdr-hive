"""Two physical hosts, one remote Hive, native Herdr in both directions.

HIVE_TEST_TARGET is an SSH test host you are authorized to use. Optional
HIVE_TEST_CONTROL is an existing private OpenSSH control socket. No credential
is stored here. All installation is disposable, in a newly-created remote /tmp
folder and local ignored .r. Requires Python pexpect and prebuilt binaries.
"""
import hashlib
import io
import json
import os
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import sys
import tarfile
import time
import pexpect
import pyte

ROOT=Path(__file__).resolve().parents[2]
R=ROOT/'.r'
TARGET=os.environ['HIVE_TEST_TARGET']
SSH=['ssh','-o','BatchMode=yes']
if os.environ.get('HIVE_TEST_CONTROL'): SSH+=['-S',os.environ['HIVE_TEST_CONTROL']]
BASE={k:v for k,v in os.environ.items() if not k.startswith(('HERDR_','BEE_'))}
HERDR=shutil.which('herdr')
remote=None
processes=[]
terminals=[]
logs=[]
result={}
class Screen(pyte.Screen):
    def report_device_status(self,*args,**kwargs):pass

class TerminalScreen:
    def __init__(self):
        self.screen=Screen(120,30);self.stream=pyte.Stream(self.screen)
    def write(self,text):self.stream.feed(text)
    def flush(self):pass
    def contains(self,text):return text in '\n'.join(self.screen.display)

q=shlex.quote


def run(args,env=None,check=True,timeout=40,input=None):
    p=subprocess.run([str(a) for a in args],env=env or BASE,input=input,capture_output=True,timeout=timeout)
    if check and p.returncode: raise RuntimeError(p.stderr.decode(errors='replace'))
    return p


def remote_run(command,**kw): return run(SSH+[TARGET,command],**kw)


def wait(fn,seconds=15):
    end=time.monotonic()+seconds
    while time.monotonic()<end:
        if fn():return
        time.sleep(.1)
    raise RuntimeError('condition timed out')


def env_command(env,args):return 'env '+' '.join(q(k+'='+v) for k,v in env.items())+' '+' '.join(q(str(a)) for a in args)


def remote_spawn(args,label,env):
    command=env_command(env,['setsid']+args)+f' </dev/null >{q(remote+"/"+label+".log")} 2>&1 & echo $!'
    return int(remote_run(command).stdout)


def api(path,method,params=None):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(5);s.connect(str(path));s.sendall((json.dumps({'id':'test','method':method,'params':params or {}})+'\n').encode());r=json.loads(s.makefile('rb').readline())
        if 'error' in r:raise RuntimeError(r)
        return r['result']


def main():
    global remote
    R.mkdir(mode=0o700)
    for name in ['local','remote','hive','bin']:(R/name).mkdir(mode=0o700)
    for name in ['local','remote']:run(['ssh-keygen','-q','-t','ed25519','-N','','-f',R/name/'key'])
    (R/'authorized_keys').write_text(''.join((R/n/'key.pub').read_text() for n in ['local','remote']))
    remote=remote_run('mktemp -d /tmp/herdr-hive.XXXXXXXX').stdout.decode().strip()
    if not remote.startswith('/tmp/herdr-hive.'):raise RuntimeError('unexpected temporary path')
    files={
        'bin/hive':ROOT/'dist/hive-linux-amd64',
        'bin/bee':ROOT/'dist/bee-linux-amd64',
        'bin/herdr':ROOT/'.local/downloads/herdr-linux-x86_64',
        'identity':R/'remote/key',
        'authorized_keys':R/'authorized_keys',
    }
    archive=io.BytesIO()
    with tarfile.open(fileobj=archive,mode='w') as tar:
        for name,path in files.items():tar.add(path,arcname=name)
    remote_run(f'tar -xf - -C {q(remote)}',input=archive.getvalue())
    remote_run(f'chmod 700 {q(remote)}/bin/*; mkdir -m 700 {q(remote)}/hive')
    port=int(remote_run("python3 -c 'import socket;s=socket.socket();s.bind((\"0.0.0.0\",0));print(s.getsockname()[1])'").stdout)
    hostname=TARGET.rsplit('@',1)[-1]
    hive_pid=remote_spawn([remote+'/bin/hive','--state-dir',remote+'/hive','--authorized-keys',remote+'/authorized_keys','--listen',f'0.0.0.0:{port}'],'hive',{})
    wait(lambda:remote_run(f'test -f {q(remote)}/hive/host_key',check=False).returncode==0)
    pub=remote_run(f'ssh-keygen -y -f {q(remote)}/hive/host_key').stdout.decode().strip()
    known=R/'known_hosts';known.write_text(f'[{hostname}]:{port} {pub}\n[127.0.0.1]:{port} {pub}\n')
    remote_run(f'cat >{q(remote)}/known_hosts',input=known.read_bytes())
    conf='onboarding = false\n[terminal]\ndefault_shell = "/bin/sh"\nshell_mode = "non_login"\n[update]\nversion_check = false\nmanifest_check = false\n'
    localenv=dict(BASE,XDG_CONFIG_HOME=str(R/'local'),XDG_STATE_HOME=str(R/'local/state'),BEE_CONFIG_DIR=str(R/'local/bee'),TERM='xterm-256color')
    remoteenv={'XDG_CONFIG_HOME':remote+'/config','XDG_STATE_HOME':remote+'/state','BEE_CONFIG_DIR':remote+'/bee','BEE_HERDR_BINARY':remote+'/bin/herdr','TERM':'xterm-256color'}
    (R/'local/herdr').mkdir();(R/'local/herdr/config.toml').write_text(conf)
    remote_run(f'mkdir -p {q(remote)}/config/herdr; cat >{q(remote)}/config/herdr/config.toml',input=conf.encode())
    log=(R/'local-herdr.log').open('w');logs.append(log)
    p=subprocess.Popen([HERDR,'server'],env=localenv,stdin=subprocess.DEVNULL,stdout=log,stderr=log,start_new_session=True);processes.append(p)
    sock=R/'local/herdr/herdr.sock';wait(sock.exists)
    remote_spawn([remote+'/bin/herdr','server'],'herdr',remoteenv)
    wait(lambda:remote_run(f'test -S {q(remote)}/config/herdr/herdr.sock',check=False).returncode==0)
    api(sock,'workspace.create',{'cwd':str(R/'local'),'label':'LOCAL_PUBLISHER','focus':True})
    remote_run(env_command(remoteenv,[remote+'/bin/herdr','workspace','create','--cwd',remote,'--label','REMOTE_PUBLISHER']))
    bee=ROOT/'dist/bee'
    run([bee,'configure','--hive',f'{hostname}:{port}','--identity',R/'local/key','--known-hosts',known],localenv)
    run([bee,'share','default'],localenv)
    localshare=json.loads(run([bee,'enable'],localenv).stdout)['shares'][0]
    remote_run(env_command(remoteenv,[remote+'/bin/bee','configure','--hive',f'127.0.0.1:{port}','--identity',remote+'/identity','--known-hosts',remote+'/known_hosts']))
    remote_run(env_command(remoteenv,[remote+'/bin/bee','share','default']))
    remoteshare=json.loads(remote_run(env_command(remoteenv,[remote+'/bin/bee','enable'])).stdout)['shares'][0]
    sshbase=['ssh','-F','/dev/null','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o',f'UserKnownHostsFile={known}','-i',str(R/'local/key'),'-p',str(port)]
    listing=json.loads(run(sshbase+['hive@'+hostname,'list --json']).stdout)
    assert [s['id'] for s in listing]==[remoteshare['id']]
    assert run(sshbase+[localshare['id']+'@'+hostname,'command -v herdr'],check=False).returncode!=0
    result['own_publication_hidden_and_rejected']=True
    for local in [True,False]:
        share=remoteshare if local else localshare
        path=str(R) if local else remote
        host=hostname if local else '127.0.0.1'
        identity=str(R/'local/key') if local else remote+'/identity'
        config=f'Host shared-peer\n HostName {host}\n Port {port}\n User {share["id"]}\n IdentityFile {identity}\n UserKnownHostsFile {path}/known_hosts\n BatchMode yes\n StrictHostKeyChecking yes\n IdentitiesOnly yes\n ControlMaster no\n'
        wrapper='#!/usr/bin/env python3\nimport os,sys\na=sys.argv[1:];out=[]\nwhile a:\n x=a.pop(0)\n if x in ("-F","-S"): a.pop(0)\n else: out.append(x)\nos.execv("/usr/bin/ssh",["ssh","-F",'+repr(path+'/ssh_config')+']+out)\n'
        if local:
            (R/'ssh_config').write_text(config);(R/'bin/ssh').write_text(wrapper);(R/'bin/ssh').chmod(0o700)
        else:
            remote_run(f'cat >{q(remote)}/ssh_config',input=config.encode());remote_run(f'cat >{q(remote)}/bin/ssh',input=wrapper.encode());remote_run(f'chmod 700 {q(remote)}/bin/ssh')
    localenv['PATH']=str(R/'bin')+os.pathsep+BASE['PATH']
    remoteenv['PATH']=remote+'/bin:/usr/local/bin:/usr/bin:/bin'
    run([HERDR,'machine','add','shared-peer','--label','Remote publisher'],localenv)
    remote_run(env_command(remoteenv,[remote+'/bin/herdr','machine','add','shared-peer','--label','Local publisher']))
    result['native_machine_add_both_directions']=True
    localtui=pexpect.spawn(HERDR,['--remote','shared-peer'],env=localenv,encoding='utf-8',timeout=25,dimensions=(30,120));terminals.append(localtui)
    rendered=TerminalScreen();localtui.logfile_read=rendered;localtui.delaybeforesend=0
    localtui.expect('REMOTE_PUBLISHER');time.sleep(.4);localtui.send("printf '\\114\\101\\116_REMOTE_OK\\n'\r");localtui.expect('LAN_REMOTE_OK')
    remotetui=pexpect.spawn(SSH[0],SSH[1:]+['-tt',TARGET,env_command(remoteenv,[remote+'/bin/herdr','--remote','shared-peer'])],encoding='utf-8',timeout=25,dimensions=(30,120));terminals.append(remotetui)
    remotetui.expect('LOCAL_PUBLISHER');time.sleep(.4);remotetui.send("printf '\\114\\101\\116_LOCAL_OK\\n'\r");remotetui.expect('LAN_LOCAL_OK')
    result['native_terminal_duplex_across_two_hosts']=True
    # Command completion measured from native TUI input to an unambiguous rendered marker.
    latencies=[]
    for i in range(20):
        marker='PROBE_'+str(i)+'_DONE'
        start=time.perf_counter();localtui.send("printf '\\120\\122\\117\\102\\105_"+str(i)+"_DONE\\n'\r")
        deadline=time.monotonic()+10
        while not rendered.contains(marker):
            if time.monotonic()>deadline:raise RuntimeError('terminal marker timeout')
            try:localtui.read_nonblocking(65536,timeout=.2)
            except pexpect.TIMEOUT:pass
        latencies.append((time.perf_counter()-start)*1000)
    result['command_to_terminal_p95_ms']=round(sorted(latencies)[18],2)
    result['latency_samples']=20
    result['load']='one active native terminal; no bulk output; ANSI terminal reconstructed; send delay disabled'
    snapshot=json.loads(remote_run(env_command({},[remote+'/bin/hive','inspect','--state-dir',remote+'/hive','--json'])).stdout)
    result['hive_memory']={k:snapshot[k] for k in ['heap_bytes','runtime_bytes','connections','channels','max_connections','max_channels']}
    for terminal in terminals:terminal.close(force=True)
    terminals.clear()
    remote_run(f'kill {hive_pid}')
    wait(lambda: not json.loads(run([bee,'status'],localenv).stdout)['connected'])
    assert api(sock,'session.snapshot')
    remote_spawn([remote+'/bin/hive','--state-dir',remote+'/hive','--authorized-keys',remote+'/authorized_keys','--listen',f'0.0.0.0:{port}'],'hive-restarted',{})
    wait(lambda:json.loads(run([bee,'status'],localenv).stdout)['connected'],35)
    wait(lambda:json.loads(remote_run(env_command(remoteenv,[remote+'/bin/bee','status'])).stdout)['connected'],35)
    assert json.loads(run([bee,'status'],localenv).stdout)['shares'][0]['id']==localshare['id']
    assert json.loads(remote_run(env_command(remoteenv,[remote+'/bin/bee','status'])).stdout)['shares'][0]['id']==remoteshare['id']
    result['hive_restart_reconnects_with_stable_ids']=True
    run([bee,'disable'],localenv)
    remote_run(env_command(remoteenv,[remote+'/bin/bee','disable']))
    result['passed']=True


if R.exists():raise RuntimeError('Refusing to reuse existing .r')
try:
    main()
finally:
    for p in terminals:p.close(force=True)
    if (R/'local/bee').exists():
        run([ROOT/'dist/bee','disable'],dict(BASE,BEE_CONFIG_DIR=str(R/'local/bee')),check=False)
    try:api(R/'local/herdr/herdr.sock','server.stop')
    except (OSError,RuntimeError):pass
    for p in processes:
        if p.poll() is None:p.terminate()
        p.wait(timeout=10)
    for f in logs:f.close()
    if remote:
        cleanupenv={'XDG_CONFIG_HOME':remote+'/config','XDG_STATE_HOME':remote+'/state','BEE_CONFIG_DIR':remote+'/bee'}
        remote_run(env_command(cleanupenv,[remote+'/bin/bee','disable']),check=False)
        remote_run(env_command(cleanupenv,[remote+'/bin/herdr','server','stop']),check=False)
        # Only processes whose executable is inside our freshly allocated directory.
        cleanup=f'''python3 - <<'PY'
import os,signal,shutil,time
root={remote!r}
for name in os.listdir('/proc'):
 if name.isdigit():
  try:
   exe=os.readlink('/proc/'+name+'/exe')
   if exe.startswith(root+'/bin/'):os.kill(int(name),signal.SIGTERM)
  except (FileNotFoundError,PermissionError,ProcessLookupError):pass
time.sleep(.3)
shutil.rmtree(root)
PY'''
        remote_run(cleanup)
    if R.exists():shutil.rmtree(R)
    result['temporary_installation_removed']=True
    print(json.dumps(result,indent=2))
