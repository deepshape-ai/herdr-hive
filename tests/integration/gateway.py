"""Independent generation-1 client used by the disposable native acceptance test."""
import json
import os
import selectors
import struct
import subprocess
import time


def number(n):
    if n < 251: return bytes([n])
    if n <= 65535: return b'\xfb' + struct.pack('<H', n)
    if n <= 4294967295: return b'\xfc' + struct.pack('<I', n)
    return b'\xfd' + struct.pack('<Q', n)


def text(s):
    p=s.encode();return number(len(p))+p


class Wire:
    def __init__(self, args):
        self.p=subprocess.Popen(args,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        self.buf=b'';self.snapshot=None;self.surface=None
        self.sel=selectors.DefaultSelector();self.sel.register(self.p.stdout,selectors.EVENT_READ)

    def close(self):
        self.p.terminate()
        try:self.p.wait(timeout=5)
        except subprocess.TimeoutExpired:self.p.kill();self.p.wait()
        self.sel.close()

    def send(self,p):
        self.p.stdin.write(struct.pack('<I',len(p))+p);self.p.stdin.flush()

    def control(self,kind,data):self.send(b'\x14'+text(kind)+text(json.dumps(data)))

    def read(self,deadline):
        while len(self.buf)<4 or len(self.buf)<4+struct.unpack('<I',self.buf[:4])[0]:
            assert time.monotonic()<deadline,'gateway response timeout'
            assert self.sel.select(max(0,deadline-time.monotonic())),'gateway response timeout'
            chunk=os.read(self.p.stdout.fileno(),65536)
            assert chunk,'gateway closed: '+self.p.stderr.read().decode()
            self.buf+=chunk
        n=struct.unpack('<I',self.buf[:4])[0];p=self.buf[4:4+n];self.buf=self.buf[4+n:]
        return p

    def until(self,predicate,timeout=15):
        deadline=time.monotonic()+timeout
        while True:
            p=self.read(deadline);d=Decoder(p);tag=d.number();v=None
            if tag==20:
                kind=d.text();v=json.loads(d.text())
                if kind=='shell.snapshot.v1':self.snapshot=v
            elif tag==13:
                self.surface=(d.text(),d.number(),d.number(),p)
            elif tag==18:
                boot=d.text();rid=d.text();final=d.number();v=json.loads(d.text())
                assert final==1 and boot==self.snapshot['boot_id']
            if predicate(tag,v):return v

    def hello(self, active=True):
        self.control('endpoint.hello.v1',{'generation':1,'cell_width_px':8,'cell_height_px':16,
            'surface_size':{'cols':100,'rows':30},'pixel_mouse':False,'direct_graphics':False,
            'endpoint_keybindings':False,'mouse_capture':True,'surface_active':active,
            'snapshot_codecs':['shell.snapshot.v1'],'surface_codecs':['shell.surface.v1'],
            'input_codecs':['shell.input.semantic.v1'],'blob_codecs':['shell.blob.v1']})
        self.until(lambda tag,v:tag==20 and v.get('generation')==1)

    def request(self,rid,method,params):
        self.send(b'\x0f'+text(self.snapshot['boot_id'])+text(json.dumps({'id':rid,'method':method,'params':params})))
        return self.until(lambda tag,v:tag==18 and v['id']==rid)


class Decoder:
    def __init__(self,b):self.b=b;self.p=0
    def number(self):
        n=self.b[self.p];self.p+=1
        if n<251:return n
        fmt,size={251:('<H',2),252:('<I',4),253:('<Q',8)}[n]
        n=struct.unpack(fmt,self.b[self.p:self.p+size])[0];self.p+=size;return n
    def text(self):
        n=self.number();s=self.b[self.p:self.p+n].decode();self.p+=n;return s


def verify(sshbase, shares, api, paths, set_enabled=None):
    w=Wire([str(a) for a in sshbase]+['hive@127.0.0.1','exec /herdr remote-client-bridge'])
    try:
        w.hello()
        w.until(lambda tag,v:tag==20 and len(v.get('workspaces',[]))==3)
        snap=w.snapshot
        workspaces=snap['workspaces'];panes=snap['panes']
        assert len({x['workspace_id'] for x in workspaces})==3
        assert len({x['pane_id'] for x in panes})==3
        for who in 'abc':
            expected=f'[{shares[who]["name"]}] HOST_{who}'
            workspace=next(x for x in workspaces if x['label']==expected)
            pane=next(x for x in panes if x['workspace_id']==workspace['workspace_id'])
            response=w.request('focus-'+who,'workspace.focus',{'workspace_id':workspace['workspace_id']})
            assert 'error' not in response,response
            assert w.snapshot['focused_workspace_id']==workspace['workspace_id']
            marker='AGGREGATE_'+who+'_ONLY'
            w.send(b'\x0d'+text(pane['pane_id'])+number(1)+b'\x01'+text(f"printf '{marker}\\n'\r"))
            deadline=time.monotonic()+10
            while True:
                content=str(api(paths[who],'pane.read',{'pane_id':'w1:p1','format':'text','source':'visible'}))
                if marker in content:break
                assert time.monotonic()<deadline;time.sleep(.05)
            for other in 'abc':
                content=str(api(paths[other],'pane.read',{'pane_id':'w1:p1','format':'text','source':'visible'}))
                assert (marker in content)==(other==who),(who,other,content)
        # Namespaced IDs from two providers must never be routed together.
        response=w.request('mixed','pane.split',{'workspace_id':workspaces[0]['workspace_id'],'target_pane_id':panes[1]['pane_id']})
        assert 'error' in response
        if set_enabled:
            set_enabled('b',False)
            w.until(lambda tag,v:tag==20 and len(v.get('workspaces',[]))==2)
            assert all(not x['label'].endswith('HOST_b') for x in w.snapshot['workspaces'])
            set_enabled('b',True)
            w.until(lambda tag,v:tag==20 and len(v.get('workspaces',[]))==3)
    finally:w.close()


def verify_visibility(sshbase, expected, excluded_label=None):
    w=Wire([str(a) for a in sshbase]+['hive@127.0.0.1','exec /herdr remote-client-bridge'])
    try:
        w.hello();w.until(lambda tag,v:tag==20 and len(v.get('workspaces',[]))==expected)
        assert expected==len(w.snapshot['workspaces'])
        if excluded_label:assert all(not x['label'].endswith(excluded_label) for x in w.snapshot['workspaces'])
    finally:w.close()


def verify_dimensions(sshbase, shares, api, paths, root):
    """Measure actual PTYs, not projected cell counts or mocked wire writes."""
    seq = 0
    def request(rid, method, params):
        response = w.request(rid, method, params)
        assert "error" not in response, response
        return response

    def sizes():
        nonlocal seq
        seq += 1
        found = {}
        for who in 'abc':
            output = root / who / ('size-' + str(seq))
            api(paths[who], 'pane.send_text', {'pane_id': 'w1:p1',
                'text': 'stty size > ' + str(output) + '\r'})
            deadline = time.monotonic() + 5
            while not output.exists() or not output.read_text().strip():
                assert time.monotonic() < deadline, 'PTY size probe timed out'
                time.sleep(.05)
            found[who] = output.read_text().strip()
        return dict(zip('abc', (found[who] for who in order))) if order else found

    order = None
    w = Wire([str(a) for a in sshbase] + ['hive@127.0.0.1', 'exec /herdr remote-client-bridge'])
    try:
        baseline = sizes()
        w.hello(active=False)
        w.until(lambda tag, v: tag == 20 and len(v.get('workspaces', [])) == 3)
        assert sizes() == baseline, 'background handshake resized publishers'
        w.send(b'\x0c' + number(8) + number(16) + number(90) + number(25) + b'\x00')
        assert 'error' not in request('hidden', 'client_shell.surface.set', {'active': False})
        assert sizes() == baseline, 'hidden resize escaped'
        workspaces = {who: next(x for x in w.snapshot['workspaces'] if x['label'].endswith('HOST_' + who)) for who in 'abc'}
        first = next(who for who in 'abc' if workspaces[who]['workspace_id'] == w.snapshot['focused_workspace_id'])
        order = [first] + [who for who in 'abc' if who != first]
        baseline = dict(zip('abc', (baseline[who] for who in order)))
        workspaces = dict(zip('abc', (workspaces[who] for who in order)))
        assert 'error' not in request('show', 'client_shell.surface.set', {'active': True})
        assert 'error' not in request('barrier-a', 'workspace.focus', {'workspace_id': workspaces['a']['workspace_id']})
        a_sizes = sizes()
        assert a_sizes['a'] != baseline['a'], ('active source did not resize', a_sizes)
        assert all(a_sizes[x] == baseline[x] for x in 'bc'), ('activation resized a hidden source', a_sizes)
        w.send(b'\x0c' + number(8) + number(16) + number(80) + number(24) + b'\x00')
        assert 'error' not in request('resize-a', 'workspace.focus', {'workspace_id': workspaces['a']['workspace_id']})
        resized = sizes()
        assert resized['a'] != a_sizes['a'], ('active resize lost', resized)
        assert all(resized[x] == baseline[x] for x in 'bc'), ('resize broadcast', resized)
        assert 'error' not in request('select-b', 'workspace.focus', {'workspace_id': workspaces['b']['workspace_id']})
        b_sizes = sizes()
        assert b_sizes['b'] == resized['a'], ('new source missed cached size', b_sizes, resized)
        assert b_sizes['c'] == baseline['c']
        other = Wire([str(a) for a in sshbase] + ['hive@127.0.0.1', 'exec /herdr remote-client-bridge'])
        try:
            other.hello(active=False)
            other.until(lambda tag, v: tag == 20 and len(v.get('workspaces', [])) == 3)
            other.send(b'\x0c' + number(8) + number(16) + number(70) + number(20) + b'\x00')
            assert 'error' not in other.request('background', 'client_shell.surface.set', {'active': False})
            assert sizes() == b_sizes, 'second background viewer changed active publisher sizes'
        finally:
            other.close()
        assert 'error' not in request('hide', 'client_shell.surface.set', {'active': False})
        time.sleep(.2)
        hidden = sizes()
        w.send(b'\x0c' + number(8) + number(16) + number(60) + number(18) + b'\x00')
        time.sleep(.2)
        assert sizes() == hidden, 'hidden source still owns resize'
        print('PTY size isolation verified:', json.dumps({'baseline': baseline, 'active_a': a_sizes, 'resized_a': resized, 'active_b': b_sizes}), flush=True)
    finally:
        w.close()
