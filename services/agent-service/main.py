import json
import os
import re
from pathlib import Path
from typing import Any, Dict, List

from dotenv import load_dotenv
from fastapi import FastAPI
from pydantic import BaseModel, Field, ValidationError

load_dotenv()

app = FastAPI(title="ApprovalFlow Agent Service")

DEFAULT_POLICY_PATH = "/app/data/policy.md"
LOCAL_PROVIDER_NAME = "local-policy-retrieval-agent-v1"
GEMINI_PROVIDER_NAME = "gemini-policy-agent-v1"
ALLOWED_ROUTES = {"approve", "human_review", "reject"}


class HealthResponse(BaseModel):
    service: str
    status: str
    provider: str


class CitedRule(BaseModel):
    rule_id: str
    quote: str


class AgentRecommendation(BaseModel):
    recommended_route: str
    confidence: float
    reason: str
    cited_rules: List[CitedRule]
    provider: str = LOCAL_PROVIDER_NAME
    fallback_used: bool = False


class GeminiRecommendation(BaseModel):
    recommended_route: str = Field(..., description="approve, human_review, or reject")
    confidence: float = Field(..., ge=0.0, le=1.0)
    reason: str
    cited_rule_ids: List[str] = Field(default_factory=list)


def configured_provider() -> str:
    return os.getenv("AGENT_PROVIDER", "local").strip().lower() or "local"


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


def local_recommendation(payload: Dict[str, Any], fallback_used: bool = False, fallback_reason: str = "") -> AgentRecommendation:
    submission = payload.get("submission", {})

    total = float(submission.get("total") or 0)
    category = str(submission.get("category") or "").lower()
    vendor_known = bool(submission.get("vendorKnown", False))
    receipt_present = bool(submission.get("receiptPresent", False))
    currency = str(submission.get("currency") or "USD").upper()
    notes = str(submission.get("notes") or "").lower()

    cited_rules = relevant_policy_rules(submission)

    prefix = ""
    if fallback_used and fallback_reason:
        prefix = f"Gemini fallback used: {fallback_reason}. "

    if "alcohol-only" in notes:
        return AgentRecommendation(
            recommended_route="reject",
            confidence=0.92,
            reason=prefix + "Local policy retrieval found the alcohol-only hard-stop rule.",
            cited_rules=[r for r in cited_rules if r.rule_id in {"MEAL-03", "AUTONOMY-CEILING"}],
            provider=LOCAL_PROVIDER_NAME,
            fallback_used=fallback_used,
        )

    if "approve me" in notes:
        return AgentRecommendation(
            recommended_route="approve",
            confidence=0.99,
            reason=prefix + "The note explicitly asks for approval, so the naive recommendation is approve. The deterministic router must still enforce retrieved policy rules.",
            cited_rules=cited_rules,
            provider=LOCAL_PROVIDER_NAME,
            fallback_used=fallback_used,
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
            reason=prefix + "Local policy retrieval found rules that require human review.",
            cited_rules=cited_rules,
            provider=LOCAL_PROVIDER_NAME,
            fallback_used=fallback_used,
        )

    return AgentRecommendation(
        recommended_route="approve",
        confidence=0.90,
        reason=prefix + "Local policy retrieval found no obvious blockers for this low-risk submission.",
        cited_rules=cited_rules,
        provider=LOCAL_PROVIDER_NAME,
        fallback_used=fallback_used,
    )


def build_gemini_prompt(submission: Dict[str, Any], cited_rules: List[CitedRule]) -> str:
    rules_text = "\n".join(
        f"- {item.rule_id}: {item.quote}" for item in cited_rules
    )

    return f"""
You are an invoice and expense approval assistant.

You do not make the final approval decision. You only recommend one route:
- approve
- human_review
- reject

The deterministic policy router is the final authority.

Return only valid JSON with this exact schema:
{{
  "recommended_route": "approve | human_review | reject",
  "confidence": 0.0,
  "reason": "short explanation",
  "cited_rule_ids": ["RULE-ID"]
}}

Relevant policy rules:
{rules_text}

Submission JSON:
{json.dumps(submission, ensure_ascii=False, indent=2)}
""".strip()


def extract_json_object(text: str) -> Dict[str, Any]:
    text = text.strip()

    if text.startswith("```"):
        text = re.sub(r"^```(?:json)?", "", text, flags=re.IGNORECASE).strip()
        text = re.sub(r"```$", "", text).strip()

    try:
        return json.loads(text)
    except json.JSONDecodeError:
        match = re.search(r"\{.*\}", text, flags=re.DOTALL)
        if not match:
            raise
        return json.loads(match.group(0))


def gemini_recommendation(payload: Dict[str, Any]) -> AgentRecommendation:
    api_key = os.getenv("GEMINI_API_KEY", "").strip()
    if not api_key:
        raise RuntimeError("GEMINI_API_KEY is not configured")

    try:
        from google import genai
        from google.genai import types
    except Exception as exc:
        raise RuntimeError(f"google-genai is unavailable: {exc}") from exc

    submission = payload.get("submission", {})
    cited_rules = relevant_policy_rules(submission)
    prompt = build_gemini_prompt(submission, cited_rules)

    model = os.getenv("GEMINI_MODEL", "gemini-2.5-flash").strip() or "gemini-2.5-flash"
    timeout_seconds = float(os.getenv("AGENT_LLM_TIMEOUT_SECONDS", "8"))

    client = genai.Client(api_key=api_key)

    response = client.models.generate_content(
        model=model,
        contents=prompt,
        config=types.GenerateContentConfig(
            temperature=0.1,
            response_mime_type="application/json",
            http_options=types.HttpOptions(timeout=int(timeout_seconds * 1000)),
        ),
    )

    text = (response.text or "").strip()
    if not text:
        raise RuntimeError("Gemini returned an empty response")

    parsed = GeminiRecommendation.model_validate(extract_json_object(text))

    route = parsed.recommended_route.strip().lower()
    if route not in ALLOWED_ROUTES:
        raise RuntimeError(f"Gemini returned unsupported route: {parsed.recommended_route}")

    cited_by_id = {item.rule_id: item for item in cited_rules}
    final_citations: List[CitedRule] = []
    for rule_id in parsed.cited_rule_ids:
        if rule_id in cited_by_id:
            final_citations.append(cited_by_id[rule_id])

    if not final_citations:
        final_citations = cited_rules[:3]

    return AgentRecommendation(
        recommended_route=route,
        confidence=parsed.confidence,
        reason=parsed.reason,
        cited_rules=final_citations,
        provider=GEMINI_PROVIDER_NAME,
        fallback_used=False,
    )


@app.get("/healthz", response_model=HealthResponse)
def healthz() -> HealthResponse:
    return HealthResponse(
        service="agent-service",
        status="ok",
        provider=configured_provider(),
    )


@app.post("/recommend", response_model=AgentRecommendation)
def recommend(payload: Dict[str, Any]) -> AgentRecommendation:
    provider = configured_provider()

    if provider == "gemini":
        try:
            return gemini_recommendation(payload)
        except (Exception, ValidationError) as exc:
            return local_recommendation(
                payload,
                fallback_used=True,
                fallback_reason=str(exc),
            )

    return local_recommendation(payload)
