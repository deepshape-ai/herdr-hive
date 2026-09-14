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

    def hello(self):
        self.control('endpoint.hello.v1',{'generation':1,'cell_width_px':8,'cell_height_px':16,
            'surface_size':{'cols':100,'rows':30},'pixel_mouse':False,'direct_graphics':False,
            'endpoint_keybindings':False,'mouse_capture':True,'surface_active':True,
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
