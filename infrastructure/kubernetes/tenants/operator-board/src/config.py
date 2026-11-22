"""Settings, all from the environment (operator-board-config ConfigMap)."""
import os

ARGOCD_NAMESPACE = os.getenv("ARGOCD_NAMESPACE", "argocd")
ROOT_APP = os.getenv("ROOT_APP", "smallworlds-root")
# Applications sourced from here run the base; the docs links follow their commit.
BASE_REPO_URL = os.getenv("BASE_REPO_URL", "https://github.com/stephan271/smallworlds.git").rstrip("/")
RING0_BOARD_URL = os.getenv("RING0_BOARD_URL", "https://stephan271.github.io/smallworlds/")

# Every source is read again this often. The page calls the Board stale when
# its document is older than three of these (design §5).
REFRESH_SECONDS = int(os.getenv("REFRESH_SECONDS", "30"))
# A source that does not answer within this is reported as not answering.
SOURCE_TIMEOUT = int(os.getenv("SOURCE_TIMEOUT", "5"))
# A sync or other operation running longer than this is shown amber (design §4.2).
SLOW_OPERATION_MINUTES = int(os.getenv("SLOW_OPERATION_MINUTES", "10"))

STATIC_DIR = os.getenv("STATIC_DIR", "/static")
PORT = int(os.getenv("PORT", "8080"))
BIND = os.getenv("BIND", "0.0.0.0")
