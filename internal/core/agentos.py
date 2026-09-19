"""AgentOS sandbox SDK. Capabilities are checked by the host broker."""
import json
import socket

def call(method):
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
        conn.settimeout(2)
        conn.connect("/input/broker/broker.sock")
        conn.sendall(json.dumps({"method": method}).encode() + b"\n")
        with conn.makefile("rb") as stream:
            response = json.loads(stream.readline(1024 * 1024 + 1024))
    if "error" in response:
        raise PermissionError(response["error"])
    return response["result"]

def context():
    return call("context.get")

def memory():
    return call("memory.read")
