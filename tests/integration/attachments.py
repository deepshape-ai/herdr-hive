"""Disposable real-Herdr regression for issue #1; no existing sessions or keys.

Run after make package. Optional BEE_NATIVE_TEST_BINARY/HIVE_NATIVE_TEST_BINARY
select binaries for old-versus-new comparisons. Herdr is resolved from PATH.
"""
import sys
sys.dont_write_bytecode = True
import fcntl
import json
import os
from pathlib import Path
import shutil
import socket
import struct
import subprocess
import tempfile
import termios
import time

from gateway import SocketWire, Wire, number

ROOT = Path(__file__).resolve().parents[2]
HERDR = shutil.which('herdr')
BEE = Path(os.environ.get('BEE_NATIVE_TEST_BINARY', ROOT / 'dist/bee'))
HIVE = Path(os.environ.get('HIVE_NATIVE_TEST_BINARY', ROOT / 'dist/hive'))
BASE = {k: v for k, v in os.environ.items() if not k.startswith(('HERDR_', 'BEE_'))}


def main(root):
    processes, logs, viewers, ptys = [], [], [], []
    socket_path = root / 'o/herdr/herdr.sock'

    def run(args, env=None):
        p = subprocess.run([str(a) for a in args], env=env or BASE, capture_output=True, text=True, timeout=30)
        assert p.returncode == 0, p.stderr
        return p.stdout

    def spawn(args, label, env=None, stdin=None):
        log = (root / (label + '.log')).open('w')
        logs.append(log)
        p = subprocess.Popen([str(a) for a in args], env=env or BASE,
                             stdin=stdin or subprocess.DEVNULL, stdout=log, stderr=log)
        processes.append(p)
        return p

    def wait(check, seconds=10):
        end = time.monotonic() + seconds
        while not check():
            assert time.monotonic() < end, 'native attachment condition timed out'
            time.sleep(.05)

    def controllers():
        found = set()
        for line in run(['ps', '-axo', 'pid=,ppid=,args=']).splitlines():
            fields = line.split(None, 2)
            if len(fields) == 3 and int(fields[1]) == publisher.pid and 'terminal session control' in fields[2]:
                found.add(int(fields[0]))
        return found

    def inspect():
        return json.loads(run([HIVE, 'inspect', '--state-dir', state, '--json']))

    def api(method, params=None):
        with socket.socket(socket.AF_UNIX) as s:
            s.settimeout(5)
            s.connect(str(socket_path))
            s.sendall((json.dumps({'id': 'attachments', 'method': method, 'params': params or {}}) + '\n').encode())
            result = json.loads(s.makefile('rb').readline())
            assert 'error' not in result, result
            return result['result']

    def sizes():
        return [struct.unpack('HHHH', fcntl.ioctl(f.fileno(), termios.TIOCGWINSZ, b'\0' * 8)) for f in ptys]

    def open_viewer():
        w = Wire(ssh)
        viewers.append(w)
        w.hello(size=(110, 40))
        w.until(lambda tag, value: tag in (13, 19) and 'ATTACH_READY' in w.screen.text())
        return w

    try:
        for who in ('owner', 'viewer'):
            run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', root / (who + '-key')])
        keys = root / 'keys'
        keys.write_text(''.join((root / (who + '-key.pub')).read_text() for who in ('owner', 'viewer')))
        with socket.socket() as s:
            s.bind(('127.0.0.1', 0))
            port = s.getsockname()[1]
        state = root / 'hs'
        spawn([HIVE, '--state-dir', state, '--authorized-keys', keys, '--listen', f'127.0.0.1:{port}'], 'hive')
        wait(lambda: (state / 'host_key').exists())
        known = root / 'known'
        known.write_text(f'[127.0.0.1]:{port} ' + run(['ssh-keygen', '-y', '-f', state / 'host_key']).strip() + '\n')
        conf = root / 'o/herdr'
        conf.mkdir(parents=True)
        (conf / 'config.toml').write_text('onboarding = false\n[terminal]\ndefault_shell = "/bin/sh"\nshell_mode = "non_login"\n[update]\nversion_check = false\nmanifest_check = false\n')
        owner = dict(BASE, XDG_CONFIG_HOME=str(root / 'o'), XDG_STATE_HOME=str(root / 's'), BEE_CONFIG_DIR=str(root / 'b'))
        spawn([HERDR, 'server'], 'herdr', owner)
        wait(socket_path.exists)
        api('workspace.create', {'cwd': str(root), 'label': 'ATTACHMENTS', 'focus': True})
        second = api('pane.split', {'target_pane_id': 'w1:p1', 'direction': 'right'})['pane']['pane_id']
        third = api('pane.split', {'target_pane_id': 'w1:p1', 'direction': 'right'})['pane']['pane_id']
        fourth = api('pane.split', {'target_pane_id': second, 'direction': 'right'})['pane']['pane_id']
        for pane in ('w1:p1', second, third, fourth):
            api('pane.split', {'target_pane_id': pane, 'direction': 'down'})
        local = SocketWire(socket_path)
        viewers.append(local)
        local.hello(size=(444, 70))
        local.until(lambda tag, value: tag == 20 and value.get('focused_tab_id'))
        local.request('owner-focus', 'tab.focus', {'tab_id': local.snapshot['focused_tab_id']})
        api('pane.send_text', {'pane_id': 'w1:p1', 'text': "printf '\\101\\124\\124\\101\\103\\110_READY\\n'\r"})
        pane_order = []
        for pane in api('pane.list')['panes']:
            pid = api('pane.process_info', {'pane_id': pane['pane_id']})['process_info']['shell_pid']
            tty = run(['ps', '-p', str(pid), '-o', 'tty=']).strip()
            assert tty and tty not in ('?', '??') and not tty.startswith('/') and '..' not in tty
            ptys.append(open('/dev/' + tty, 'rb', buffering=0))
            pane_order.append(pane['pane_id'])
        assert len(ptys) == 8
        baseline = sizes()
        run([BEE, 'configure', '--hive', f'127.0.0.1:{port}', '--identity', root / 'owner-key', '--known-hosts', known], owner)
        run([BEE, 'share', 'default'], owner)
        config_path = root / 'b/config.json'
        config = json.loads(config_path.read_text())
        config['enabled'] = True
        config_path.write_text(json.dumps(config))
        publisher = spawn([BEE, 'run'], 'bee', owner)
        wait(lambda: json.loads(run([BEE, 'status'], owner)).get('connected'))
        ssh = ['/usr/bin/ssh', '-F', '/dev/null', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
               '-o', f'UserKnownHostsFile={known}', '-o', 'IdentitiesOnly=yes', '-i', str(root / 'viewer-key'),
               '-p', str(port), 'hive@127.0.0.1', 'exec /herdr remote-client-bridge']
        w = open_viewer()
        assert sizes() == baseline, 'initial sharing changed complete PTY geometry'
        initial_controllers = controllers()
        assert len(initial_controllers) == 8, 'view did not pin all eight terminals'
        for _ in range(100):
            w.close()
            viewers.remove(w)
            w = open_viewer()
            assert sizes() == baseline, 'viewer reconnect changed PTY geometry'
            assert controllers() == initial_controllers, 'rapid reconnect rebuilt sizing controllers'
        attached = len(initial_controllers)
        w.close()
        viewers.remove(w)
        local.send(b'\x0c' + number(8) + number(16) + number(320) + number(60) + b'\x00')
        local.request('owner-resize', 'tab.focus', {'tab_id': local.snapshot['focused_tab_id']})
        wait(lambda: all(current[:2] != old[:2] for current, old in zip(sizes(), baseline)))
        wait(lambda: not controllers())

        terminal = api('pane.get', {'pane_id': 'w1:p1'})['pane']['terminal_id']
        controller = spawn([HERDR, 'terminal', 'session', 'control', terminal, '--cols', '80', '--rows', '24'],
                           'controller', dict(owner, HERDR_SOCKET_PATH=str(socket_path)), subprocess.PIPE)
        controlled_index = pane_order.index('w1:p1')
        wait(lambda: sizes()[controlled_index][:2] == (24, 80))
        protected = sizes()[controlled_index]
        before_failure = inspect()['shares'][0]['bytes_to_publisher']
        blocked = Wire(ssh)
        viewers.append(blocked)
        blocked.hello(size=(110, 40))
        wait(lambda: inspect()['shares'][0]['bytes_to_publisher'] > before_failure and inspect()['gateway_upstreams'] == 0)
        failed_traffic = inspect()['shares'][0]['bytes_to_publisher']
        deadline = time.monotonic() + 12
        while time.monotonic() < deadline:
            try:
                blocked.read(min(deadline, time.monotonic() + .5))
            except AssertionError as error:
                assert 'response timeout' in str(error), error
        assert inspect()['shares'][0]['bytes_to_publisher'] == failed_traffic, 'persistent controller failure reopened the source'
        assert blocked.p.poll() is None, 'source failure closed the consumer transport'
        assert sizes()[controlled_index] == protected, 'existing controller was displaced'
        controller.terminate()
        controller.wait(timeout=5)
        print(json.dumps({'herdr': run([HERDR, '--version']).strip(), 'rapid_reconnections': 100,
                          'terminal_controllers': attached, 'persistent_failure_retried': False,
                          'full_pty_geometry_preserved': True, 'passed': True}))
    finally:
        for w in viewers:
            try:
                w.close()
            except (OSError, subprocess.SubprocessError):
                pass
        if socket_path.exists():
            try:
                api('server.stop')
            except (OSError, AssertionError):
                pass
        for p in reversed(processes):
            if p.poll() is None:
                p.terminate()
            try:
                p.wait(timeout=10)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
            if p.stdin:
                p.stdin.close()
        for f in ptys + logs:
            f.close()


if __name__ == '__main__':
    assert HERDR and BEE.is_file() and HIVE.is_file(), 'Herdr and built Bee/Hive required'
    with tempfile.TemporaryDirectory(prefix='ha-', dir='/tmp') as directory:
        main(Path(directory))
