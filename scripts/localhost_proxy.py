#!/usr/bin/env python3
"""
Simple loopback proxy:
Forwards connections from 127.0.0.1:8101 (localhost) to 127.10.1.1:8101 (Node 1 Gateway)
so users can open http://localhost:8101/dashboard in any browser.
"""

import socket
import threading
import sys

TARGET_HOST = "127.10.1.1"
TARGET_PORT = 8101
LISTEN_HOST = "127.0.0.1"
LISTEN_PORT = 8101

def forward(src, dst):
    try:
        while True:
            data = src.recv(8192)
            if not data:
                break
            dst.sendall(data)
    except Exception:
        pass
    finally:
        try:
            src.close()
        except Exception:
            pass
        try:
            dst.close()
        except Exception:
            pass

def handle_client(client_sock):
    target_sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        target_sock.connect((TARGET_HOST, TARGET_PORT))
    except Exception as e:
        client_sock.close()
        return

    threading.Thread(target=forward, args=(client_sock, target_sock), daemon=True).start()
    threading.Thread(target=forward, args=(target_sock, client_sock), daemon=True).start()

def main():
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        s.bind((LISTEN_HOST, LISTEN_PORT))
    except Exception as e:
        print(f"Proxy bind failed on {LISTEN_HOST}:{LISTEN_PORT}: {e}", file=sys.stderr)
        return
    s.listen(25)
    print(f"Proxy forwarding {LISTEN_HOST}:{LISTEN_PORT} -> {TARGET_HOST}:{TARGET_PORT}", flush=True)

    while True:
        try:
            client, _ = s.accept()
            threading.Thread(target=handle_client, args=(client,), daemon=True).start()
        except KeyboardInterrupt:
            break
        except Exception as e:
            print(f"Proxy accept error: {e}", file=sys.stderr)

if __name__ == "__main__":
    main()
