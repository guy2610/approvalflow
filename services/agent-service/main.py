import os
import re
from pathlib import Path
from typing import Any, Dict, List

from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI(title="ApprovalFlow Agent Service")

DEFAULT_POLICY_PATH = "/app/data/policy.md"


class HealthResponse(BaseModel):
    service: str
    status: str


class CitedRule(BaseModel):
    rule_id: str
    quote: str


class AgentRecommendation(BaseModel):
    recommended_route: str
    confidence: float
    reason: str
    cited_rules: List[CitedRule]
    provider: str = "local-policy-retrieval-agent-v1"


def load_policy_text() -> str:
    policy_path = Path(os.getenv("POLICY_PATH", DEFAULT_POLICY_PATH))
    try:
        return policy_path.read_text(encoding="utf-8")
    except FileNotFoundError:
        return ""


def clean_markdown(value: str) -> str:
    value = value.replace("**", "")
    value = value.replace("*", "")
    value = value.replace("`", "")
    value = value.replace("→", "->")
    return " ".join(value.split())


def parse_policy_rules(policy_text: str) -> Dict[str, str]:
    rules: Dict[str, str] = {}

    for line in policy_text.splitlines():
        stripped = line.strip()
        if not stripped.startswith("| `"):
            continue

        parts = [part.strip() for part in stripped.strip("|").split("|")]
        if len(parts) < 2:
            continue

        rule_id = parts[0].strip().strip("`")
        rule_text = clean_markdown(parts[1])

        if rule_id and rule_id != "rule_id":
            rules[rule_id] = rule_text

    return rules


POLICY_TEXT = load_policy_text()
POLICY_RULES = parse_policy_rules(POLICY_TEXT)


def rule(rule_id: str, fallback: str) -> CitedRule:
    return CitedRule(rule_id=rule_id, quote=POLICY_RULES.get(rule_id, fallback))


def append_rule_once(rules: List[CitedRule], rule_id: str, fallback: str) -> None:
    if any(existing.rule_id == rule_id for existing in rules):
        return
    rules.append(rule(rule_id, fallback))


def relevant_policy_rules(submission: Dict[str, Any]) -> List[CitedRule]:
    total = float(submission.get("total") or 0)
    category = str(submission.get("category") or "").lower()
    vendor_known = bool(submission.get("vendorKnown", False))
    receipt_present = bool(submission.get("receiptPresent", False))
    currency = str(submission.get("currency") or "USD").upper()
    notes = str(submission.get("notes") or "").lower()

    cited_rules: List[CitedRule] = []

    # Global hard stops and autonomy rules are relevant to almost every decision.
    append_rule_once(
        cited_rules,
        "AUTONOMY-CEILING",
        "The agent may auto-approve only when the USD amount is within the configured autonomy ceiling.",
    )
    append_rule_once(
        cited_rules,
        "AUTONOMY-CONFIDENCE",
        "The agent may auto-approve only when confidence is above the configured threshold.",
    )

    if not vendor_known:
        append_rule_once(
            cited_rules,
            "GLOBAL-VENDOR",
            "A new or unknown vendor is always reviewed by a human.",
        )

    if total > 25 and not receipt_present:
        append_rule_once(
            cited_rules,
            "GLOBAL-RECEIPT",
            "A receipt is required for any expense over $25.",
        )

    if currency != "USD":
        append_rule_once(
            cited_rules,
            "GLOBAL-FX",
            "Foreign-currency items are converted to USD and may require human review.",
        )

    if category == "meals":
        append_rule_once(
            cited_rules,
            "MEAL-01",
            "Personal/team meals are reimbursable up to $75 per attendee.",
        )

        if total > 500 or "client" in notes:
            append_rule_once(
                cited_rules,
                "MEAL-02",
                "Client entertainment over $500 requires justification and client name.",
            )

        if "alcohol-only" in notes:
            append_rule_once(
                cited_rules,
                "MEAL-03",
                "Alcohol-only receipts are not reimbursable.",
            )

    elif category == "travel":
        append_rule_once(
            cited_rules,
            "TRAVEL-01",
            "Economy flights, standard hotels, and standard/economy ground transport are policy-eligible.",
        )

        if total > 1500:
            append_rule_once(
                cited_rules,
                "TRAVEL-02",
                "Any single travel expense over $1,500 requires manager approval.",
            )

        if "business class" in notes or "first class" in notes or "first-class" in notes:
            append_rule_once(
                cited_rules,
                "TRAVEL-03",
                "First/business-class travel always requires approval.",
            )

    elif category == "saas":
        append_rule_once(
            cited_rules,
            "SAAS-01",
            "Subscriptions are policy-eligible up to $200 / month.",
        )

    elif category == "hardware":
        append_rule_once(
            cited_rules,
            "HW-01",
            "Hardware purchases are policy-eligible up to $1,000.",
        )

        if total > 1000:
            append_rule_once(
                cited_rules,
                "HW-02",
                "Hardware over $1,000 is a capital expense and requires human approval.",
            )

    else:
        append_rule_once(
            cited_rules,
            "AUTONOMY-HARDSTOPS",
            "Hard stops force human review regardless of amount or confidence.",
        )

    return cited_rules


@app.get("/healthz", response_model=HealthResponse)
def healthz() -> HealthResponse:
    return HealthResponse(service="agent-service", status="ok")


@app.post("/recommend", response_model=AgentRecommendation)
def recommend(payload: Dict[str, Any]) -> AgentRecommendation:
    submission = payload.get("submission", {})

    total = float(submission.get("total") or 0)
    category = str(submission.get("category") or "").lower()
    vendor_known = bool(submission.get("vendorKnown", False))
    receipt_present = bool(submission.get("receiptPresent", False))
    currency = str(submission.get("currency") or "USD").upper()
    notes = str(submission.get("notes") or "").lower()

    cited_rules = relevant_policy_rules(submission)

    if "alcohol-only" in notes:
        return AgentRecommendation(
            recommended_route="reject",
            confidence=0.92,
            reason="Local policy retrieval found the alcohol-only hard-stop rule.",
            cited_rules=[r for r in cited_rules if r.rule_id in {"MEAL-03", "AUTONOMY-CEILING"}],
        )

    # Intentional prompt-injection demo:
    # the agent can be tricked by invoice notes, but the deterministic router must still enforce policy.
    if "approve me" in notes:
        return AgentRecommendation(
            recommended_route="approve",
            confidence=0.99,
            reason="The note explicitly asks for approval, so the naive recommendation is approve. The deterministic router must still enforce retrieved policy rules.",
            cited_rules=cited_rules,
        )

    human_review_conditions = [
        not vendor_known,
        total > 250,
        total > 25 and not receipt_present,
        currency != "USD" and total > 1000,
        category == "saas" and total > 200,
        category == "hardware" and total > 1000,
    ]

    if any(human_review_conditions):
        return AgentRecommendation(
            recommended_route="human_review",
            confidence=0.86,
            reason="Local policy retrieval found rules that require human review.",
            cited_rules=cited_rules,
        )

    return AgentRecommendation(
        recommended_route="approve",
        confidence=0.90,
        reason="Local policy retrieval found no obvious blockers for this low-risk submission.",
        cited_rules=cited_rules,
    )
