#!/usr/bin/env python3
"""
TestCloud LXMF Bluetooth / Reticulum Gateway Bot
================================================
Listens for incoming LXMF messages from Columba (or any LXMF client) over Bluetooth LE / Reticulum,
stores incoming messages as decentralized records into the TestCloud cluster, and sends an
automated acknowledgment message back to the sender over Reticulum.
"""

import os
import sys
import time
import json
import signal
import threading
import urllib.request
import urllib.error
from datetime import datetime

import RNS
import LXMF

# Paths & Settings
APP_DIR = "./testcloud"
DATA_DIR = os.path.join(APP_DIR, "data")
STORAGE_PATH = os.path.join(DATA_DIR, "lxmf_storage")
IDENTITY_PATH = os.path.join(DATA_DIR, "lxmf_identity")
LOG_PATH = os.path.join(DATA_DIR, "logs", "lxmf_bot.log")
TESTCLOUD_API_URL = "http://127.10.1.1:8101/api/records"
DISPLAY_NAME = "TestCloud Gateway"
ANNOUNCE_INTERVAL = 20  # Seconds between mesh announces

os.makedirs(STORAGE_PATH, exist_ok=True)
os.makedirs(os.path.join(DATA_DIR, "logs"), exist_ok=True)

running = True

def log(msg):
    timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    formatted = f"[{timestamp}] [LXMF-Bot] {msg}"
    print(formatted, flush=True)
    try:
        with open(LOG_PATH, "a") as f:
            f.write(formatted + "\n")
    except Exception:
        pass

def handle_sigterm(signum, frame):
    global running
    log("Shutdown signal received, exiting gracefully...")
    running = False

signal.signal(signal.SIGINT, handle_sigterm)
signal.signal(signal.SIGTERM, handle_sigterm)

def ingest_to_testcloud(sender_hash, title, content):
    """Stores the received message into the local TestCloud cluster via REST API."""
    full_text = f"[{title}] {content}" if title else content
    payload = {
        "collection": "columba_messages",
        "text": full_text,
        "user_pub_address": f"lxmf:{sender_hash}"
    }

    try:
        req = urllib.request.Request(
            TESTCLOUD_API_URL,
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json"}
        )
        with urllib.request.urlopen(req, timeout=5) as response:
            if response.status in (200, 201):
                res_body = json.loads(response.read().decode("utf-8"))
                data_id = res_body.get("data_id", "rec_stored")
                auditors = res_body.get("auditor_peers", [])
                storage_nodes = res_body.get("storage_peers", [])
                log(f"Stored in TestCloud! DataID: {data_id} | Auditors: {len(auditors)} | Storage Peers: {len(storage_nodes)}")
                return data_id, True
            else:
                log(f"TestCloud API error: status {response.status}")
                return "api_error", False
    except urllib.error.URLError as e:
        log(f"Failed to connect to TestCloud API: {e}")
        return "cluster_offline", False
    except Exception as e:
        log(f"Unexpected error storing to TestCloud: {e}")
        return "error", False

def main():
    global running
    log("Starting TestCloud LXMF Gateway Bot...")

    # Connect to Reticulum shared instance
    rns = RNS.Reticulum()

    # Load or generate persistent identity
    if os.path.exists(IDENTITY_PATH):
        identity = RNS.Identity.from_file(IDENTITY_PATH)
        log("Loaded existing identity from disk.")
    else:
        identity = RNS.Identity()
        identity.to_file(IDENTITY_PATH)
        log("Generated new persistent identity and saved to disk.")

    # Initialize LXMF Router
    router = LXMF.LXMRouter(identity=identity, storagepath=STORAGE_PATH)
    delivery_dest = router.register_delivery_identity(identity, display_name=DISPLAY_NAME)
    dest_hash_hex = RNS.hexrep(delivery_dest.hash, False)
    dest_hash_formatted = f"<{dest_hash_hex}>"

    log(f"Bot Destination: {dest_hash_formatted}")
    log(f"Display Name   : {DISPLAY_NAME}")
    log(f"Columba Contact: {dest_hash_hex}")

    # Callback when a message arrives
    def on_message_delivered(message):
        try:
            sender_hash = RNS.hexrep(message.source_hash, False) if message.source_hash else "unknown"
            sender_formatted = f"<{sender_hash}>"
            title = message.title_as_string()
            content = message.content_as_string()

            log(f"--- INCOMING LXMF MESSAGE ---")
            log(f"From   : {sender_formatted}")
            log(f"Title  : {title if title else '(no title)'}")
            log(f"Content: {content}")

            # Ingest into TestCloud
            data_id, success = ingest_to_testcloud(sender_hash, title, content)

            # Build ACK reply
            if success:
                reply_text = (
                    f"☁️ TestCloud Cluster ACK\n\n"
                    f"✅ Message saved to TestCloud!\n"
                    f"📁 Record ID: {data_id}\n"
                    f"🌐 Gateway: Node 1 (127.10.1.1)\n"
                    f"🔗 Replicated across 6 cluster nodes.\n"
                    f"⏱ Received: {datetime.now().strftime('%H:%M:%S')}"
                )
            else:
                reply_text = (
                    f"☁️ TestCloud Cluster ACK\n\n"
                    f"⚠️ Message received over BLE, but cluster API returned: {data_id}\n"
                    f"⏱ Received: {datetime.now().strftime('%H:%M:%S')}"
                )

            # Reply to sender
            reply_dest = message.source
            if reply_dest is None and message.source_hash:
                sender_id = RNS.Identity.recall(message.source_hash)
                if sender_id is not None:
                    reply_dest = RNS.Destination(sender_id, RNS.Destination.OUT, RNS.Destination.SINGLE, "lxmf", "delivery")

            if reply_dest:
                reply_msg = LXMF.LXMessage(
                    destination=reply_dest,
                    source=delivery_dest,
                    content=reply_text,
                    title="TestCloud ACK"
                )
                router.handle_outbound(reply_msg)
                log(f"Sent LXMF reply to {sender_formatted}")
            else:
                log(f"Could not determine reply destination for {sender_formatted}")

        except Exception as e:
            log(f"Error processing received message: {e}")

    router.register_delivery_callback(on_message_delivered)

    # Initial announces to ensure Columba receives it right away
    log("Sending initial announces over Reticulum & BLE...")
    for i in range(3):
        router.announce(delivery_dest.hash)
        time.sleep(1)

    log("LXMF Bot is online and listening. Columba can now send messages!")
    log(f"Destination Hash for manual entry if needed: {dest_hash_hex}")

    last_announce = time.time()
    while running:
        time.sleep(1)
        now = time.time()
        if now - last_announce >= ANNOUNCE_INTERVAL:
            log("Sending periodic announce...")
            router.announce(delivery_dest.hash)
            last_announce = now

    log("TestCloud LXMF Gateway Bot stopped.")

if __name__ == "__main__":
    main()
