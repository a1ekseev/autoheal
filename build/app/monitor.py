import docker
from ping3 import ping
import httpx
import time
import os
import logging

# Настройки из переменных окружения
CHECK_INTERVAL = int(os.getenv("CHECK_INTERVAL", "10"))
FAIL_THRESHOLD = int(os.getenv("FAIL_THRESHOLD", "3"))
PING_TIMEOUT = float(os.getenv("PING_TIMEOUT", "10"))
CURL_TIMEOUT = float(os.getenv("CURL_TIMEOUT", "10"))
LOG_LEVEL = os.getenv("LOG_LEVEL", "INFO").upper()

# Логгер
logging.basicConfig(
    level=getattr(logging, LOG_LEVEL, logging.INFO),
    format="%(asctime)s [%(levelname)s] %(message)s"
)
logger = logging.getLogger(__name__)

failure_counts = {}

def monitor_containers():
    client = docker.DockerClient(base_url="unix://var/run/docker.sock")
    containers = client.containers.list()

    for container in containers:
        labels = container.labels
        if labels.get("autoheal.monitor.enable", "false").lower() != "true":
            continue

        container_id = container.id
        name = container.name
        logger.info(f"Monitoring container: {name}")
        fail = False

        if "autoheal.monitor.ping" in labels:
            host = labels["autoheal.monitor.ping"]
            result = ping(host, timeout=PING_TIMEOUT)
            if result is None:
                logger.warning(f"[PING FAIL] {name}: No response from {host}")
                fail = True
            else:
                logger.info(f"[PING OK] {name}: {host} responded in {result * 1000:.2f} ms")

        elif "autoheal.monitor.curl" in labels:
            url = labels["autoheal.monitor.curl"]
            expected_response = labels.get("autoheal.monitor.curl.response", "")
            try:
                response = httpx.get(url, timeout=CURL_TIMEOUT)
                if expected_response not in response.text:
                    logger.warning(f"[CURL FAIL] {name}: Unexpected response from {url}")
                    fail = True
                else:
                    logger.info(f"[CURL OK] {name}: Response matched")
            except Exception as e:
                logger.error(f"[CURL ERROR] {name}: Failed to request {url} — {e}")
                fail = True

        if fail:
            failure_counts[container_id] = failure_counts.get(container_id, 0) + 1
            if failure_counts[container_id] >= FAIL_THRESHOLD:
                logger.error(f"[RESTARTING] {name}: Exceeded {FAIL_THRESHOLD} failures, restarting container")
                try:
                    container.restart()
                    failure_counts[container_id] = 0
                except Exception as e:
                    logger.exception(f"[ERROR] Failed to restart {name}: {e}")
        else:
            failure_counts[container_id] = 0

if __name__ == "__main__":
    logger.info(
        f"Starting monitor with CHECK_INTERVAL={CHECK_INTERVAL}s, "
        f"FAIL_THRESHOLD={FAIL_THRESHOLD}, "
        f"PING_TIMEOUT={PING_TIMEOUT}s, CURL_TIMEOUT={CURL_TIMEOUT}s"
    )
    while True:
        monitor_containers()
        time.sleep(CHECK_INTERVAL)