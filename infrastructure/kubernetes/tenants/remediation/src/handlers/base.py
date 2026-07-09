"""Handler contract for the Tier 1 registry.

A handler matches a class of alert and takes one bounded, deterministic action
(here: open a PR). It never mutates the cluster directly. The outcome tells the
registry whether the alert was resolved-in-progress or should escalate.
"""
from dataclasses import dataclass
from enum import Enum


class Outcome(str, Enum):
    ACTED = "acted"                    # a fix was proposed (PR opened / dry-run logged)
    NOOP = "noop"                      # matched, but no action needed (already correct)
    NOT_APPLICABLE = "not_applicable"  # this handler does not apply -> try next / escalate
    ERROR = "error"                    # handler blew up -> escalate


@dataclass
class Result:
    outcome: Outcome
    detail: str = ""

    @property
    def handled(self) -> bool:
        """Did this handler take responsibility for the alert (act or no-op)?"""
        return self.outcome in (Outcome.ACTED, Outcome.NOOP)


class Handler:
    """Base class. Subclasses set `name` and implement matches()/handle()."""
    name: str = "handler"

    def matches(self, alert: dict) -> bool:
        raise NotImplementedError

    def handle(self, alert: dict) -> Result:
        raise NotImplementedError
