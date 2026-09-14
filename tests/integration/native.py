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
sys.dont_write_bytecode = True

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
    install=R/'install';install.mkdir()
    for component in ('hive','bee'):
        run(['go','-C',ROOT/component,'build','-ldflags','-X main.version=0.0.1',
             '-o',install/component,'./cmd/'+component],timeout=90)
    if os.environ.get('BEE_NATIVE_TEST_BINARY'):
        shutil.copy2(os.environ['BEE_NATIVE_TEST_BINARY'], install/'bee')
    initial_bee_version=run([install/'bee','version']).stdout.strip()
    for who in ['hive', 'a', 'b', 'c', 'd', 'consumer']:
        (R / who).mkdir(mode=0o700)
    for who in ['a', 'b', 'c', 'd', 'consumer']:
        run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', R / who / 'key'])
    keys = R / 'authorized_keys'
    keys.write_text(''.join((R / w / 'key.pub').read_text() for w in ['a', 'b', 'c', 'consumer']))
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0)); port = s.getsockname()[1]
    tokens=R/'authorized_tokens'
    issued=json.loads(run([install/'hive','enroll','issue','--tokens',tokens,'--max-uses','1']).stdout)
    hive = spawn([install / 'hive', '--state-dir', R/'hive', '--authorized-keys', keys,
                  '--authorized-tokens',tokens,'--listen', f'127.0.0.1:{port}'], 'hive')
    wait(lambda: (R/'hive/host_key').exists())
    pub = run(['ssh-keygen', '-y', '-f', R/'hive/host_key']).stdout.strip()
    known = R/'known_hosts'; known.write_text(f'[127.0.0.1]:{port} {pub}\n')
    # Device d is absent from the external allowlist. Configure registers before saving.
    denv=dict(BASE,BEE_CONFIG_DIR=str(R/'d/bee'))
    dargs=[ROOT/'dist/bee','configure','--hive',f'127.0.0.1:{port}','--identity',R/'d/key','--known-hosts',known,'--token',issued['secret']]
    run(dargs,denv);run(dargs,denv)
    saved=(R/'d/bee/config.json').read_bytes()
    assert issued['secret'].encode() not in saved
    assert (R/'d/key.pub').read_text().split()[1] in (R/'hive/registered_keys').read_text()
    dssh=['/usr/bin/ssh','-F','/dev/null','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o',f'UserKnownHostsFile={known}','-i',R/'d/key','-p',str(port),'hive@127.0.0.1','list --json']
    assert json.loads(run(dssh).stdout)==[]
    other=list(dargs);other[5]=R/'consumer/key'
    assert run(other,denv,check=False).returncode!=0
    assert (R/'d/bee/config.json').read_bytes()==saved
    run([install/'hive','enroll','revoke',issued['id'],'--tokens',tokens])
    assert run(dargs,denv,check=False).returncode!=0
    assert (R/'d/bee/config.json').read_bytes()==saved
    expired=json.loads(run([install/'hive','enroll','issue','--tokens',tokens,'--ttl','1ns']).stdout)
    expiredargs=list(dargs);expiredargs[-1]=expired['secret']
    assert run(expiredargs,denv,check=False).returncode!=0
    assert (R/'d/bee/config.json').read_bytes()==saved
    inspect=json.loads(run([install/'hive','inspect','--state-dir',R/'hive','--json']).stdout)
    assert inspect['enroll_enabled'] and inspect['enrolled']==2 and inspect['enroll_rejected']==3
    result['enrollment_idempotency_limits_expiry_revocation_config_atomicity']=True
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
        spawn([install/'bee','run'],who+'-bee',env)
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
    with sshconfig.open('a') as f:f.write(f'Host hive\n HostName 127.0.0.1\n Port {port}\n User hive\n IdentityFile {R}/consumer/key\n UserKnownHostsFile {known}\n IdentitiesOnly yes\n StrictHostKeyChecking yes\n BatchMode yes\n ControlMaster no\n')
    bindir=R/'bin';bindir.mkdir();wrapper=bindir/'ssh'
    wrapper.write_text('#!'+sys.executable+'\nimport os,sys\na=sys.argv[1:];out=[]\nwhile a:\n x=a.pop(0)\n if x in ("-F","-S"): a.pop(0)\n else: out.append(x)\nos.execv("/usr/bin/ssh",["ssh","-F",'+repr(str(sshconfig))+']+out)\n');wrapper.chmod(0o700)
    consumer_env=dict(BASE,XDG_CONFIG_HOME=str(R/'consumer'),XDG_STATE_HOME=str(R/'consumer/state'),PATH=str(bindir)+os.pathsep+BASE['PATH'],TERM='xterm-256color')
    conf=R/'consumer/herdr';conf.mkdir();(conf/'config.toml').write_text('onboarding = false\n[update]\nversion_check = false\nmanifest_check = false\n')
    from gateway import verify, verify_visibility, verify_dimensions, verify_shared_sizing
    verify_dimensions(sshbase, shares, api, {who:R/who/'herdr/herdr.sock' for who in 'abc'}, R)
    result['gateway_background_and_active_pty_size_isolation']=True
    verify_shared_sizing(sshbase, shares, api, {who:R/who/'herdr/herdr.sock' for who in 'abc'}, R)
    result['shared_pty_sizing_local_and_two_remotes']=True
    verify(sshbase,shares,api,{who:R/who/'herdr/herdr.sock' for who in 'abc'},
           lambda who,enabled:run([ROOT/'dist/bee','enable' if enabled else 'disable'],envs[who]))
    own=list(sshbase);own[own.index('-i')+1]=str(R/'a/key')
    verify_visibility(own,2,'HOST_a')
    result['gateway_prefix_ids_focus_input_self_hiding_and_reconnect']=True
    added=run([BIN,'machine','add','hive','--label','Hive'],consumer_env,check=False,timeout=45)
    assert added.returncode==0,added.stderr
    p=pexpect.spawn(BIN,['--remote','hive'],env=consumer_env,encoding='utf-8',timeout=20,dimensions=(40,150))
    terminals.append(p);p.expect('HOST_');time.sleep(1)
    p.send("printf '\\110\\111\\126\\105_GATEWAY_OK\\n'\r");p.expect('HIVE_GATEWAY_OK');p.close(force=True)
    # Subsequent direct-sharing tests run without an additional saved gateway.
    entry=next(x for x in json.loads(run([BIN,'machine','list','--json'],consumer_env).stdout) if x['label']=='Hive')
    run([BIN,'machine','remove',entry['id']],consumer_env)
    result['native_gateway_machine_add_and_terminal']=True
    added=run([BIN,'machine','add','shared-b','--label','Worker B'],consumer_env,check=False,timeout=45)
    result['machine_add']={'code':added.returncode,'stdout':added.stdout,'stderr':added.stderr}
    if added.returncode:raise RuntimeError(added.stderr)
    listed=json.loads(run([BIN,'machine','list','--json'],consumer_env).stdout)
    assert listed[0]['label']=='Worker B'
    # A native remote TUI sends actual terminal input and receives output through Hive.
    # Use a viewport containing the pinned source grid; small windows deliberately
    # crop it and are covered by the independent PTY sizing tests above.
    for who in 'abc':
        p=pexpect.spawn(BIN,['--remote','shared-'+who],env=consumer_env,encoding='utf-8',timeout=20,dimensions=(50,170))
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
    tui=pexpect.spawn(str(ROOT/'dist/bee'),['tui'],env=dict(envs['a'],TERM='xterm-256color'),encoding='utf-8',timeout=10,dimensions=(30,120));terminals.append(tui)
    tui.expect('Worker')
    # Connection starts on Hive; Tab selects the name and Enter starts editing.
    tui.send('\t\r')
    tui.sendcontrol('a');tui.sendcontrol('k');tui.send('Updated worker')
    tui.sendcontrol('s');tui.expect_exact('Changes saved.')
    tui.send('q');tui.expect(pexpect.EOF)
    assert json.loads(run([ROOT/'dist/bee','name'],envs['a']).stdout)['name']=='Updated worker'
    result['plugin_actions_and_tui_cli_parity']=True
    # Online process replacement keeps configuration, identities and local sessions.
    before=json.loads(run([ROOT/'dist/hive','inspect','--state-dir',R/'hive','--json']).stdout)
    assert before['version']=='0.0.1'
    expected=run([ROOT/'dist/hive','version']).stdout.strip()
    for component in ('hive','bee'):
        shutil.copy2(ROOT/'dist'/component,install/(component+'.new'))
        os.replace(install/(component+'.new'),install/component)
    with socket.socket(socket.AF_UNIX) as control:
        control.settimeout(5);control.connect(str(R/'hive/control.sock'))
        control.sendall(b'"restart"\n');assert json.loads(control.recv(128)) is True
    def hive_restarted():
        p=run([ROOT/'dist/hive','inspect','--state-dir',R/'hive','--json'],check=False)
        return p.returncode==0 and json.loads(p.stdout)['version']==expected and json.loads(p.stdout)['pid']==before['pid']
    wait(hive_restarted)
    assert hive.poll() is None
    for who in 'abc':
        def reconnected():
            state=json.loads(run([ROOT/'dist/bee','status'],envs[who]).stdout)
            return state.get('connected') and state['shares'][0]['id']==shares[who]['id']
        wait(reconnected,20)
    before_bee=json.loads(run([ROOT/'dist/bee','status'],envs['c']).stdout)
    assert before_bee['version']==initial_bee_version
    expected_bee=run([ROOT/'dist/bee','version']).stdout.strip()
    with socket.socket(socket.AF_UNIX) as control:
        control.settimeout(10);control.connect(str(R/'c/bee/control.sock'))
        control.sendall(b'"restart"\n');control.recv(65536)
    def bee_restarted():
        state=json.loads(run([ROOT/'dist/bee','status'],envs['c']).stdout)
        return state.get('connected') and state.get('version')==expected_bee and state.get('pid')==before_bee['pid'] and state['shares'][0]['id']==shares['c']['id']
    wait(bee_restarted,20)
    assert api(R/'a/herdr/herdr.sock','session.snapshot')
    p=pexpect.spawn(BIN,['--remote','shared-c'],env=consumer_env,encoding='utf-8',timeout=20,dimensions=(50,170));terminals.append(p)
    p.expect('HOST_c');time.sleep(.4)
    p.send("printf '\\125\\120\\107\\122\\101\\104\\105_OK\\n'\r")
    p.expect('UPGRADE_OK')
    result['hive_and_bee_reexec_preserve_local_sessions_and_share_ids']=True
    result['native_terminal_after_executable_upgrade']=True
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
