package main

import (
	"net/http"

	"approvalflow/internal/platform/httpx"
)

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>ApprovalFlow Demo UI</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <style>
    body { font-family: system-ui, sans-serif; margin: 24px; background: #f6f8fb; color: #162033; }
    h1, h2 { margin-bottom: 8px; }
    .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(340px, 1fr)); gap: 16px; }
    .card { background: white; border: 1px solid #d9e2ef; border-radius: 12px; padding: 16px; }
    label { display: block; font-weight: 600; margin-top: 12px; margin-bottom: 4px; }
    input, textarea, button { width: 100%; box-sizing: border-box; font: inherit; border-radius: 8px; border: 1px solid #c9d5e6; padding: 8px; }
    textarea { min-height: 180px; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; }
    button { margin-top: 12px; border: 0; background: #1d4ed8; color: white; font-weight: 700; cursor: pointer; }
    button.secondary { background: #475569; }
    button.danger { background: #b91c1c; }
    pre { white-space: pre-wrap; overflow-wrap: anywhere; background: #0f172a; color: #e2e8f0; border-radius: 12px; padding: 16px; min-height: 180px; }
    .small { color: #64748b; font-size: 0.92rem; }
  </style>
</head>
<body>
  <h1>ApprovalFlow Demo UI</h1>
  <p class="small">Minimal local demo interface for submissions, human approvals, and audit trails.</p>

  <div class="grid">
    <section class="card">
      <h2>Submit invoice</h2>
      <label for="correlationId">Correlation ID</label>
      <input id="correlationId" value="ui-demo-correlation">

      <label for="invoicePayload">Invoice JSON</label>
      <textarea id="invoicePayload">{
  "id": "INV-1001",
  "vendor": "Cafe Good",
  "invoiceNumber": "MEAL-1001",
  "category": "meal",
  "currency": "USD",
  "total": 42,
  "tax": 0,
  "lineItems": [
    {
      "description": "Team lunch",
      "amount": 42
    }
  ],
  "attendees": 1,
  "receiptAttached": true,
  "notes": "Team lunch"
}</textarea>

      <button onclick="submitInvoice()">Submit</button>
    </section>

    <section class="card">
      <h2>Submission status</h2>
      <label for="trackingId">Tracking ID</label>
      <input id="trackingId" placeholder="sub_...">
      <button onclick="getSubmission()">Get status</button>
    </section>

    <section class="card">
      <h2>Approvals</h2>
      <button onclick="listApprovals()">List approvals</button>

      <label for="approvalTrackingId">Approval tracking ID</label>
      <input id="approvalTrackingId" placeholder="sub_...">

      <label for="approvalReason">Reason</label>
      <input id="approvalReason" value="Approved from demo UI.">

      <button onclick="approvalAction('approve')">Approve</button>
      <button class="danger" onclick="approvalAction('reject')">Reject</button>
      <button class="secondary" onclick="approvalAction('request-info')">Request info</button>
    </section>

    <section class="card">
      <h2>Audit trail</h2>
      <label for="auditCorrelationId">Correlation ID</label>
      <input id="auditCorrelationId" value="ui-demo-correlation">
      <button onclick="getAudit()">Get audit</button>
    </section>
  </div>

  <section class="card" style="margin-top:16px">
    <h2>Output</h2>
    <pre id="output">Ready.</pre>
  </section>

<script>
function output(value) {
  const text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  document.getElementById("output").textContent = text;
}

function headers(correlationId) {
  return {
    "Content-Type": "application/json",
    "X-Correlation-Id": correlationId || "ui-demo-correlation"
  };
}

async function parseResponse(response) {
  const text = await response.text();
  let payload = {};
  try {
    payload = text ? JSON.parse(text) : {};
  } catch (err) {
    payload = { raw: text };
  }

  return {
    status: response.status,
    statusText: response.statusText,
    correlationId: response.headers.get("X-Correlation-Id"),
    rateLimit: response.headers.get("X-RateLimit-Limit"),
    rateLimitRemaining: response.headers.get("X-RateLimit-Remaining"),
    payload: payload
  };
}

async function submitInvoice() {
  try {
    const correlationId = document.getElementById("correlationId").value;
    const raw = document.getElementById("invoicePayload").value;
    JSON.parse(raw);

    const response = await fetch("/submissions", {
      method: "POST",
      headers: headers(correlationId),
      body: raw
    });

    const result = await parseResponse(response);
    if (result.payload && result.payload.tracking_id) {
      document.getElementById("trackingId").value = result.payload.tracking_id;
      document.getElementById("approvalTrackingId").value = result.payload.tracking_id;
    }
    output(result);
  } catch (err) {
    output("Submit failed: " + err.message);
  }
}

async function getSubmission() {
  try {
    const trackingId = document.getElementById("trackingId").value.trim();
    if (!trackingId) {
      output("Missing tracking ID.");
      return;
    }

    const response = await fetch("/submissions/" + encodeURIComponent(trackingId), {
      method: "GET",
      headers: headers("ui-status-" + trackingId)
    });

    output(await parseResponse(response));
  } catch (err) {
    output("Get submission failed: " + err.message);
  }
}

async function listApprovals() {
  try {
    const response = await fetch("/approvals", {
      method: "GET",
      headers: headers("ui-approvals")
    });

    output(await parseResponse(response));
  } catch (err) {
    output("List approvals failed: " + err.message);
  }
}

async function approvalAction(action) {
  try {
    const trackingId = document.getElementById("approvalTrackingId").value.trim();
    if (!trackingId) {
      output("Missing approval tracking ID.");
      return;
    }

    const payload = {
      actor: "demo-ui@approvalflow.local",
      reason: document.getElementById("approvalReason").value || "Updated from demo UI."
    };

    const response = await fetch("/approvals/" + encodeURIComponent(trackingId) + "/" + action, {
      method: "POST",
      headers: headers("ui-approval-" + trackingId + "-" + action),
      body: JSON.stringify(payload)
    });

    output(await parseResponse(response));
  } catch (err) {
    output("Approval action failed: " + err.message);
  }
}

async function getAudit() {
  try {
    const correlationId = document.getElementById("auditCorrelationId").value.trim();
    if (!correlationId) {
      output("Missing correlation ID.");
      return;
    }

    const response = await fetch("/audit/" + encodeURIComponent(correlationId), {
      method: "GET",
      headers: headers("ui-audit-" + correlationId)
    });

    output(await parseResponse(response));
  } catch (err) {
    output("Get audit failed: " + err.message);
  }
}
</script>
</body>
</html>`

func (s *server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		httpx.WriteError(w, r, http.StatusNotFound, "not found")
		return
	}

	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(indexHTML))
}
