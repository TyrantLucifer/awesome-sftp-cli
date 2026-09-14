#!/usr/bin/python3
# Delay each SFTP response by a fixed RTT while preserving pipelining.
# Local sftp-server, no SSH/network/credentials, bounded response queue.
import sys, os, struct, subprocess, threading, queue, time, json
delay=float(sys.argv[1] if len(sys.argv)>1 else os.environ['AMSFTP_PERFORMANCE_RTT'])/1000
stats_path=sys.argv[2] if len(sys.argv)>2 else os.environ['AMSFTP_PERFORMANCE_STATS']
server=subprocess.Popen(["/usr/lib/openssh/sftp-server"],stdin=subprocess.PIPE,stdout=subprocess.PIPE,bufsize=0)
stats={"requests":{}, "responses":{}, "write_payload_bytes":0, "read_payload_bytes":0}
pending=queue.Queue(maxsize=128)
def exact(f,n):
    out=bytearray()
    while len(out)<n:
        b=f.read(n-len(out))
        if not b: return None
        out.extend(b)
    return bytes(out)
def packets(f):
    while True:
        head=exact(f,4)
        if head is None: return
        n=struct.unpack(">I",head)[0]
        if n>256*1024: raise ValueError("oversize SFTP packet")
        body=exact(f,n)
        if body is None: return
        yield head+body
def send(f,b):
    while b:
        n=f.write(b)
        b=b[n:]
def requests():
    try:
        for packet in packets(sys.stdin.buffer):
            body=packet[4:]; typ=body[0]
            stats["requests"][str(typ)]=stats["requests"].get(str(typ),0)+1
            if typ==6:
                hlen=struct.unpack(">I",body[5:9])[0]
                stats["write_payload_bytes"]+=struct.unpack(">I",body[17+hlen:21+hlen])[0]
            send(server.stdin,packet)
    finally: server.stdin.close()
def responses():
    try:
        for packet in packets(server.stdout):
            body=packet[4:]; typ=body[0]
            stats["responses"][str(typ)]=stats["responses"].get(str(typ),0)+1
            if typ==103:
                stats["read_payload_bytes"]+=struct.unpack(">I",body[5:9])[0]
            pending.put((time.monotonic()+delay,packet))
    finally: pending.put(None)
t1=threading.Thread(target=requests);t2=threading.Thread(target=responses)
t1.start();t2.start()
while True:
    item=pending.get()
    if item is None: break
    due,packet=item
    time.sleep(max(0,due-time.monotonic()))
    send(sys.stdout.buffer,packet);sys.stdout.buffer.flush()
t1.join();t2.join();server.wait()
with open(stats_path,"w") as f: json.dump(stats,f,sort_keys=True)
