"""Three disposable Bees, real Hive/Herdr/CLI, deterministic agent executable.

No user sessions, credentials, agent accounts, or inference requests are used.
The fake codex exercises Herdr's real agent launch/detection/input/state APIs.
"""
import concurrent.futures
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
HERDR = shutil.which('herdr')
BASE = {k: v for k, v in os.environ.items() if not k.startswith(('HERDR_', 'BEE_'))}


def run(args, env, check=True, timeout=45):
    p = subprocess.run(list(map(str, args)), env=env, capture_output=True, text=True, timeout=timeout)
    if check and p.returncode:
        raise AssertionError(f'{args}: code={p.returncode}\n{p.stdout}\n{p.stderr}')
    return p


def wait(fn, timeout=15):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if fn():
            return
        time.sleep(.1)
    raise AssertionError('condition timed out')


def api(path, method, params=None):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(5)
        s.connect(str(path))
        s.sendall((json.dumps({'id': 'test', 'method': method, 'params': params or {}}) + '\n').encode())
        response = json.loads(s.makefile('rb').readline())
        if 'error' in response:
            raise AssertionError(response)
        return response['result']


def main():
    result = {}
    with tempfile.TemporaryDirectory(prefix='bee-cli-', dir='/tmp') as tmp:
        root = Path(tmp)
        processes, logs, sockets = [], [], []
        def spawn(args, label, env):
            log = (root / (label + '.log')).open('w'); logs.append(log)
            p = subprocess.Popen(list(map(str, args)), env=env, stdin=subprocess.DEVNULL,
                                 stdout=log, stderr=log, start_new_session=True)
            processes.append(p)
            return p
        try:
            bindir = root / 'bin'; bindir.mkdir()
            for component in ('bee', 'hive'):
                installed = os.environ.get(component.upper() + '_NATIVE_TEST_BINARY')
                if installed:
                    shutil.copy2(installed, bindir / component)
                else:
                    run(['go', '-C', ROOT / component, 'build', '-o', bindir / component, './cmd/' + component], BASE, timeout=120)
            # The fixture is a real foreground process launched by agent.start.
            fixture = bindir / 'codex'
            fixture.write_text('#!' + sys.executable + '\n' + r'''
import json, os, socket, sys, time
def state(value):
    with socket.socket(socket.AF_UNIX) as s:
        s.connect(os.environ['HERDR_SOCKET_PATH'])
        request={'id':'fixture','method':'pane.report_agent','params':{
            'pane_id':os.environ['HERDR_PANE_ID'],'agent':'codex',
            'source':'test:bee-cli','state':value}}
        s.sendall((json.dumps(request)+'\n').encode())
        response=json.loads(s.makefile('rb').readline())
        if 'error' in response: raise RuntimeError(response)
print('Codex fixture ready',flush=True)
state('idle')
for line in sys.stdin:
    line=line.strip().replace('\x1b[200~','').replace('\x1b[201~','')
    if not line: continue
    state('working')
    time.sleep(.5)
    print('FIXTURE_RESULT:'+line,flush=True)
    state('idle')
''')
            fixture.chmod(0o700)
            envs, shares = {}, {}
            for who in 'abc':
                home = root / who; home.mkdir()
                run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', home / 'key'], BASE)
            keys = root / 'keys'
            keys.write_text(''.join((root / who / 'key.pub').read_text() for who in 'abc'))
            with socket.socket() as s:
                s.bind(('127.0.0.1', 0)); port = s.getsockname()[1]
            spawn([bindir / 'hive', '--state-dir', root / 'hive', '--authorized-keys', keys,
                   '--listen', f'127.0.0.1:{port}'], 'hive', BASE)
            wait(lambda: (root / 'hive/host_key').exists())
            public = run(['ssh-keygen', '-y', '-f', root / 'hive/host_key'], BASE).stdout.strip()
            known = root / 'known_hosts'; known.write_text(f'[127.0.0.1]:{port} {public}\n')
            for who in 'abc':
                home = root / who
                conf = home / 'herdr'; conf.mkdir()
                (conf / 'config.toml').write_text('onboarding = false\n[terminal]\ndefault_shell = "/bin/sh"\nshell_mode = "non_login"\n[update]\nversion_check = false\nmanifest_check = false\n')
                env = dict(BASE, XDG_CONFIG_HOME=str(home), XDG_STATE_HOME=str(home / 'state'),
                           BEE_CONFIG_DIR=str(home / 'bee'), PATH=str(bindir) + os.pathsep + BASE['PATH'])
                envs[who] = env
                spawn([HERDR, 'server'], who + '-herdr', env)
                sock = conf / 'herdr.sock'; sockets.append(sock); wait(sock.exists)
                api(sock, 'workspace.create', {'cwd': str(home), 'label': 'LOCAL_' + who})
                run([bindir / 'bee', 'configure', '--hive', f'127.0.0.1:{port}', '--identity', home / 'key', '--known-hosts', known], env)
                run([bindir / 'bee', 'name', who.upper()], env)
                run([bindir / 'bee', 'share', 'default'], env)
                path = home / 'bee/config.json'
                cfg = json.loads(path.read_text()); cfg['enabled'] = True; path.write_text(json.dumps(cfg))
                spawn([bindir / 'bee', 'run'], who + '-bee', env)
                def connected():
                    state = json.loads(run([bindir / 'bee', 'status'], env).stdout)
                    if state.get('connected'):
                        shares[who] = state['shares'][0]
                        return True
                wait(connected)

            # A real unshared session exists on B; directory/API access must not
            # reach it even when the consumer inherits that session name.
            spawn([HERDR, '--session', 'private', 'server'], 'b-private-herdr', envs['b'])
            private_socket = root / 'b/herdr/sessions/private/herdr.sock'
            sockets.append(private_socket); wait(private_socket.exists)
            api(private_socket, 'workspace.create', {'cwd': str(root / 'b'), 'label': 'PRIVATE'})

            def remote(who, target, *args, check=True, timeout=45):
                # Deliberately poison the caller context: the remote wrapper must clear it.
                env = dict(envs[who], HERDR_SOCKET_PATH=str(root / who / 'herdr/herdr.sock'),
                           HERDR_SESSION='private', HERDR_PANE_ID='w1:p1', HERDR_WORKSPACE_ID='w1')
                return run([bindir / 'bee', 'on', target, '--', 'herdr', *args], env, check=check, timeout=timeout)

            for who in 'abc':
                listing = json.loads(run([bindir / 'bee', 'targets'], envs[who]).stdout)
                assert len(listing) == 2 and all(s['api'] and len(s['generation']) == 32 for s in listing)
                assert shares[who]['id'] not in [s['id'] for s in listing]
            result['directory_capabilities_and_self_hiding'] = True

            panes, workspaces = {}, {}
            for caller, target in [('a', 'b'), ('b', 'c'), ('c', 'a')]:
                selected = target.upper() + '/default'
                created = json.loads(remote(caller, selected, 'workspace', 'create', '--cwd', str(root / target), '--label', 'REMOTE_' + target).stdout)['result']
                panes[target] = created['root_pane']['pane_id']
                workspaces[target] = created['workspace']['workspace_id']
                started = json.loads(remote(caller, selected, 'agent', 'start', 'reviewer', '--kind', 'codex', '--pane', panes[target]).stdout)['result']['agent']
                assert started['name'] == 'reviewer' and started['interactive_ready']
                remote(caller, selected, 'agent', 'prompt', 'reviewer', 'from_' + caller, '--wait', '--timeout', '10000')
                output = remote(caller, selected, 'agent', 'read', 'reviewer', '--source', 'visible').stdout
                assert 'FIXTURE_RESULT:from_' + caller in output, output
                assert api(root / target / 'herdr/herdr.sock', 'agent.get', {'target': 'reviewer'})['agent']['pane_id'] == panes[target]
            result['three_way_native_agent_create_prompt_wait_read'] = True
            result['same_agent_name_isolated_by_session'] = True

            # A drives B and C concurrently; no shared mutable remote context.
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                futures = [pool.submit(remote, 'a', shares[w]['id'], 'agent', 'prompt', 'reviewer', 'parallel_' + w, '--wait', '--timeout', '10000') for w in 'bc']
                for future in futures: future.result()
            for who in 'bc':
                assert 'FIXTURE_RESULT:parallel_' + who in remote('a', shares[who]['id'], 'agent', 'read', 'reviewer', '--source', 'visible').stdout
            result['concurrent_B_C_isolation'] = True

            tab = json.loads(remote('a', 'B/default', 'tab', 'create', '--workspace', workspaces['b'], '--label', 'destination').stdout)['result']
            remote('a', 'B/default', 'pane', 'move', panes['b'], '--tab', tab['tab']['tab_id'], '--split', 'right', '--no-focus')
            moved = api(root / 'b/herdr/herdr.sock', 'agent.get', {'target': 'reviewer'})['agent']
            assert moved['tab_id'] == tab['tab']['tab_id']
            remote('a', 'B/default', 'pane', 'rename', moved['pane_id'], 'REMOTE_PANEL')
            remote('a', 'B/default', 'pane', 'zoom', '--pane', moved['pane_id'], '--on')
            remote('a', 'B/default', 'pane', 'zoom', '--pane', moved['pane_id'], '--off')
            split = json.loads(remote('a', 'B/default', 'pane', 'split', moved['pane_id'], '--direction', 'down', '--no-focus').stdout)['result']['pane']
            remote('a', 'B/default', 'pane', 'resize', '--pane', split['pane_id'], '--direction', 'up', '--amount', '0.1')
            remote('a', 'B/default', 'pane', 'close', split['pane_id'])
            result['native_tab_and_pane_management'] = True

            syntax = remote('a', 'B/default', 'agent', 'list', '--not-a-real-option', check=False)
            assert syntax.returncode == 2, syntax
            rejected = remote('a', 'B/default', 'pane', 'split', '--current', check=False)
            assert rejected.returncode != 0
            assert remote('a', 'B/default', 'agent', 'get', 'missing', check=False).returncode == 1
            assert remote('a', 'B/private', 'agent', 'list', check=False).returncode != 0
            result['native_exit_codes_and_private_target_rejection'] = True

            remote('a', 'C/default', 'pane', 'close', panes['c'])
            assert not json.loads(remote('a', 'C/default', 'agent', 'list').stdout)['result']['agents']
            run([bindir / 'bee', 'disable'], envs['b'])
            assert remote('a', shares['b']['id'], 'agent', 'list', check=False).returncode != 0
            assert api(root / 'b/herdr/herdr.sock', 'agent.get', {'target': 'reviewer'})
            result['close_and_unshare_preserve_other_agents'] = True
            result['passed'] = True
        finally:
            for sock in sockets:
                try: api(sock, 'server.stop')
                except (OSError, AssertionError, ValueError): pass
            for p in reversed(processes):
                if p.poll() is None: p.terminate()
                try: p.wait(timeout=10)
                except subprocess.TimeoutExpired: p.kill(); p.wait()
            for log in logs: log.close()
            if not result.get('passed'):
                # Local fixture-only logs; never real prompts or credentials.
                for path in root.glob('*herdr.log'):
                    print(path.name, path.read_text()[-2000:], file=sys.stderr)
            print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
