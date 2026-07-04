from typing import Any, Dict, List

from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI(title="ApprovalFlow Agent Service")


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
    provider: str = "stub-agent-v1"


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

    cited_rules: List[CitedRule] = []

    if "alcohol-only" in notes:
        cited_rules.append(
            CitedRule(
                rule_id="MEAL-03",
                quote="Alcohol-only receipts are not reimbursable.",
            )
        )
        return AgentRecommendation(
            recommended_route="reject",
            confidence=0.92,
            reason="The notes indicate an alcohol-only receipt.",
            cited_rules=cited_rules,
        )

    if not vendor_known:
        cited_rules.append(
            CitedRule(
                rule_id="GLOBAL-VENDOR",
                quote="New or unknown vendor always requires human review.",
            )
        )

    if total > 250:
        cited_rules.append(
            CitedRule(
                rule_id="AUTONOMY-CEILING",
                quote="Auto-approve only if amount is within the configured autonomy ceiling.",
            )
        )

    if total > 25 and not receipt_present:
        cited_rules.append(
            CitedRule(
                rule_id="GLOBAL-RECEIPT",
                quote="Receipt is required for expenses over $25.",
            )
        )

    if currency != "USD" and total > 1000:
        cited_rules.append(
            CitedRule(
                rule_id="GLOBAL-FX",
                quote="Foreign-currency items over threshold require human review.",
            )
        )

    if category == "saas" and total > 200:
        cited_rules.append(
            CitedRule(
                rule_id="SAAS-01",
                quote="SaaS subscriptions are eligible up to $200/month.",
            )
        )

    if category == "hardware" and total > 1000:
        cited_rules.append(
            CitedRule(
                rule_id="HW-02",
                quote="Hardware over $1,000 is capital expense and requires human approval.",
            )
        )

    if cited_rules:
        return AgentRecommendation(
            recommended_route="human_review",
            confidence=0.86,
            reason="The agent found policy conditions that require human review.",
            cited_rules=cited_rules,
        )

    return AgentRecommendation(
        recommended_route="approve",
        confidence=0.90,
        reason="The agent found no obvious policy blockers.",
        cited_rules=[
            CitedRule(
                rule_id="AUTONOMY-CEILING",
                quote="Auto-approve only if under the configured autonomy ceiling and policy-compliant.",
            )
        ],
    )
