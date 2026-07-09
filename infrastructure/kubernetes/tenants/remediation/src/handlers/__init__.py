"""Handler registry package. Add new Tier 1 handlers to REGISTRY."""
from handlers.oomkill import OOMKillHandler

# Ordered; the first handler that both matches() and returns a handled Result
# wins. Everything unmatched or declined escalates to Tier 2.
REGISTRY = [
    OOMKillHandler(),
]
