"""Disposable native Herdr acceptance test. Never uses existing sessions or SSH keys.

Build dist/hive and dist/bee first. Requires Herdr 0.9.0 and pexpect.
Temporary state stays under ignored .r and is removed on success or failure.
"""
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import time
import pexpect

ROOT = Path(__file__).resolve().parents[2]
R = ROOT / '.r'
BIN = shutil.which('herdr')
BASE = {k: v for k, v in os.environ.items() if not k.startswith(('HERDR_', 'BEE_'))}
processes, logs, terminals, sockets = [], [], [], []
result = {}


def run(args, env=None, check=True, timeout=30):
    p = subprocess.run([str(a) for a in args], env=env or BASE, capture_output=True, text=True, timeout=timeout)
    if check and p.returncode:
        raise RuntimeError(f'{args[0]} failed: {p.stderr}')
    return p


def spawn(args, label, env=None):
    f = (R / (label + '.log')).open('w'); logs.append(f)
    p = subprocess.Popen([str(a) for a in args], env=env or BASE, stdout=f, stderr=f,
                         stdin=subprocess.DEVNULL, start_new_session=True)
    processes.append(p)
    return p


def wait(test, seconds=10):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        if test(): return
        time.sleep(.05)
    raise RuntimeError('condition timed out')


def api(path, method, params=None):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(5); s.connect(str(path))
        s.sendall((json.dumps({'id': 'test', 'method': method, 'params': params or {}}) + '\n').encode())
        reply = json.loads(s.makefile('rb').readline())
        if 'error' in reply: raise RuntimeError(reply)
        return reply['result']


def main():
    R.mkdir(mode=0o700)
    for who in ['hive', 'a', 'b', 'c', 'consumer']:
        (R / who).mkdir(mode=0o700)
    for who in ['a', 'b', 'c', 'consumer']:
        run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', R / who / 'key'])
    keys = R / 'authorized_keys'
    keys.write_text(''.join((R / w / 'key.pub').read_text() for w in ['a', 'b', 'c', 'consumer']))
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0)); port = s.getsockname()[1]
    hive = spawn([ROOT / 'dist/hive', '--state-dir', R/'hive', '--authorized-keys', keys,
                  '--listen', f'127.0.0.1:{port}'], 'hive')
    wait(lambda: (R/'hive/host_key').exists())
    pub = run(['ssh-keygen', '-y', '-f', R/'hive/host_key']).stdout.strip()
    known = R/'known_hosts'; known.write_text(f'[127.0.0.1]:{port} {pub}\n')
    envs, shares = {}, {}
    for who in 'abc':
        conf = R/who/'herdr'; conf.mkdir()
        (conf/'config.toml').write_text('onboarding = false\n[terminal]\ndefault_shell = "/bin/sh"\nshell_mode = "non_login"\n[update]\nversion_check = false\nmanifest_check = false\n')
        env = dict(BASE, XDG_CONFIG_HOME=str(R/who), XDG_STATE_HOME=str(R/who/'state'), BEE_CONFIG_DIR=str(R/who/'bee'))
        envs[who] = env
        spawn([BIN, 'server'], who+'-herdr', env)
        sock = conf/'herdr.sock'; sockets.append(sock); wait(sock.exists)
        api(sock, 'workspace.create', {'cwd': str(R/who), 'label': 'HOST_'+who, 'focus': True})
        bee = ROOT/'dist/bee'
        run([bee,'configure','--hive',f'127.0.0.1:{port}','--identity',R/who/'key','--known-hosts',known],env)
        run([bee,'name','Worker'],env)
        run([bee,'share','default'],env)
        # Run the exact publisher daemon in the foreground so cleanup owns every PID.
        cpath=R/who/'bee/config.json'; cfg=json.loads(cpath.read_text());cfg['enabled']=True;cpath.write_text(json.dumps(cfg))
        spawn([bee,'run'],who+'-bee',env)
        def connected():
            p=run([bee,'status'],env); return json.loads(p.stdout).get('connected')
        wait(connected)
        shares[who] = json.loads(run([bee,'status'],env).stdout)['shares'][0]
    assert len({s['name'] for s in shares.values()}) == 3
    sshbase = ['/usr/bin/ssh','-F','/dev/null','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes',
               '-o',f'UserKnownHostsFile={known}','-o','IdentitiesOnly=yes','-i',str(R/'consumer/key'),'-p',str(port)]
    listing=json.loads(run(sshbase+['hive@127.0.0.1','list --json']).stdout)
    assert len(listing)==3
    result['name_collision_and_directory']=True
    assert run(sshbase+[shares['b']['id']+'@127.0.0.1','id'],check=False).returncode != 0
    assert run(sshbase+['s-unshared@127.0.0.1','command -v herdr'],check=False).returncode != 0
    result['shell_and_unshared_target_denied']=True
    # Isolate native OpenSSH config without modifying ~/.ssh/config or Herdr itself.
    sshconfig=R/'ssh_config'
    sshconfig.write_text(''.join(f'Host shared-{w}\n HostName 127.0.0.1\n Port {port}\n User {s["id"]}\n IdentityFile {R}/consumer/key\n UserKnownHostsFile {known}\n IdentitiesOnly yes\n StrictHostKeyChecking yes\n BatchMode yes\n ControlMaster no\n' for w,s in shares.items()))
    bindir=R/'bin';bindir.mkdir();wrapper=bindir/'ssh'
    wrapper.write_text('#!'+sys.executable+'\nimport os,sys\na=sys.argv[1:];out=[]\nwhile a:\n x=a.pop(0)\n if x in ("-F","-S"): a.pop(0)\n else: out.append(x)\nos.execv("/usr/bin/ssh",["ssh","-F",'+repr(str(sshconfig))+']+out)\n');wrapper.chmod(0o700)
    consumer_env=dict(BASE,XDG_CONFIG_HOME=str(R/'consumer'),XDG_STATE_HOME=str(R/'consumer/state'),PATH=str(bindir)+os.pathsep+BASE['PATH'],TERM='xterm-256color')
    conf=R/'consumer/herdr';conf.mkdir();(conf/'config.toml').write_text('onboarding = false\n[update]\nversion_check = false\nmanifest_check = false\n')
    added=run([BIN,'machine','add','shared-b','--label','Worker B'],consumer_env,check=False,timeout=45)
    result['machine_add']={'code':added.returncode,'stdout':added.stdout,'stderr':added.stderr}
    if added.returncode:raise RuntimeError(added.stderr)
    listed=json.loads(run([BIN,'machine','list','--json'],consumer_env).stdout)
    assert listed[0]['label']=='Worker B'
    # A native remote TUI sends actual terminal input and receives output through Hive.
    for who in 'abc':
        p=pexpect.spawn(BIN,['--remote','shared-'+who],env=consumer_env,encoding='utf-8',timeout=20,dimensions=(30,120))
        terminals.append(p)
        p.expect('HOST_'+who)
        time.sleep(.4)
        p.send("printf '\\110\\111\\126\\105_"+who+"_OK\\n'\r")
        p.expect('HIVE_'+who+'_OK')
    result['native_bidirectional_three_publishers']=True
    plugin=ROOT/'dist/packages'/('bee-'+('darwin' if sys.platform=='darwin' else 'linux')+'-'+('arm64' if os.uname().machine in ['arm64','aarch64'] else 'amd64'))
    run([BIN,'plugin','link',plugin,'--enabled'],envs['a'])
    run([BIN,'plugin','action','invoke','disable','--plugin','herdr.bee'],envs['a'])
    wait(lambda: not json.loads(run([ROOT/'dist/bee','status'],envs['a']).stdout)['enabled'])
    run([BIN,'plugin','action','invoke','enable','--plugin','herdr.bee'],envs['a'])
    wait(lambda: json.loads(run([ROOT/'dist/bee','status'],envs['a']).stdout)['connected'])
    tui=pexpect.spawn(str(ROOT/'dist/bee'),['tui'],env=envs['a'],encoding='utf-8',timeout=10,dimensions=(30,120));terminals.append(tui)
    tui.expect('Configure Hive');tui.sendline('2');tui.expect('Visible name:');tui.sendline('Updated worker');tui.expect('Updated worker');tui.sendline('q');tui.expect(pexpect.EOF)
    assert json.loads(run([ROOT/'dist/bee','name'],envs['a']).stdout)['name']=='Updated worker'
    result['plugin_actions_and_tui_cli_parity']=True
    # Disable tears down an already-open native connection; local session stays alive.
    run([ROOT/'dist/bee','disable'],envs['b'])
    assert api(R/'b/herdr/herdr.sock','session.snapshot')
    assert run(sshbase+[shares['b']['id']+'@127.0.0.1','command -v herdr'],check=False).returncode!=0
    listing=json.loads(run(sshbase+['hive@127.0.0.1','list --json']).stdout)
    assert len(listing)==2
    result['disable_disconnects_without_stopping_agent']=True
    result['inspect']=json.loads(run([ROOT/'dist/hive','inspect','--state-dir',R/'hive','--json']).stdout)
    result['passed']=True


if R.exists():
    raise RuntimeError("Refusing to reuse an existing .r directory")

try:
    main()
finally:
    for p in terminals:
        p.close(force=True)
    for who in 'abc':
        if (R/who/'bee').exists():run([ROOT/'dist/bee','disable'],dict(BASE,BEE_CONFIG_DIR=str(R/who/'bee')),check=False)
    for sock in sockets:
        try: api(sock,'server.stop')
        except (OSError,RuntimeError): pass
    for p in reversed(processes):
        if p.poll() is None: p.terminate()
        try: p.wait(timeout=10)
        except subprocess.TimeoutExpired:p.kill();p.wait()
    for f in logs:f.close()
    result['children_stopped']=all(p.poll() is not None for p in processes)
    print(json.dumps(result,indent=2))
    # Keep sanitized result, never keys, addresses of real hosts, or terminal recordings.
    if R.exists():shutil.rmtree(R)
