"use strict";

const scenarios = {
  auto: {
    invoiceId: "DEMO-AUTO-1001",
    vendor: "Cafe Good",
    amount: 42,
    recommendation: "approve",
    violations: [],
    initialReason: "Submission accepted for asynchronous processing."
  },
  review: {
    invoiceId: "DEMO-REVIEW-1003",
    vendor: "Client Steakhouse",
    amount: 1820,
    recommendation: "human_review",
    violations: ["MEAL-02", "AUTONOMY-CEILING"],
    initialReason: "Submission accepted for asynchronous processing."
  },
  "request-info": {
    invoiceId: "DEMO-INFO-1003",
    vendor: "Client Steakhouse",
    amount: 1820,
    recommendation: "human_review",
    violations: ["MEAL-02", "AUTONOMY-CEILING"],
    initialReason: "Submission accepted for asynchronous processing."
  }
};

const state = {
  selectedScenario: null,
  trackingId: "",
  correlationId: "",
  status: "",
  reason: "",
  revision: 0,
  events: [],
  metrics: {
    total: 0,
    completed: 0,
    pending: 0,
    waiting: 0,
    approvedAmount: 0,
    rejected: 0
  }
};

function byId(id) {
  return document.getElementById(id);
}

function formatUSD(value) {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD"
  }).format(Number(value || 0));
}

function createIdentifier(prefix) {
  return prefix + "_" + Math.random().toString(16).slice(2, 18);
}

function currentScenario() {
  return scenarios[state.selectedScenario] || null;
}

function addEvent(title, detail) {
  state.events.push({
    title,
    detail,
    time: new Date().toLocaleTimeString()
  });

  renderTimeline();
}

function renderTimeline() {
  const timeline = byId("timeline");
  timeline.innerHTML = "";

  if (state.events.length === 0) {
    timeline.innerHTML =
      '<li class="timeline-empty">Workflow events will appear here.</li>';
    return;
  }

  state.events.forEach(event => {
    const item = document.createElement("li");
    item.className = "timeline-item";

    const title = document.createElement("strong");
    title.textContent = event.title;

    const detail = document.createElement("span");
    detail.textContent = event.detail;

    const time = document.createElement("span");
    time.textContent = event.time;

    item.appendChild(title);
    item.appendChild(detail);
    item.appendChild(time);

    timeline.appendChild(item);
  });
}

function renderMetrics() {
  byId("metricTotal").textContent = state.metrics.total;
  byId("metricCompleted").textContent = state.metrics.completed;
  byId("metricPending").textContent = state.metrics.pending;
  byId("metricWaiting").textContent = state.metrics.waiting;
  byId("metricApprovedAmount").textContent =
    formatUSD(state.metrics.approvedAmount);
  byId("metricRejected").textContent = state.metrics.rejected;
}

function setStatus(status, reason) {
  state.status = status;
  state.reason = reason;

  byId("submissionStatus").textContent = status;
  byId("reason").textContent = reason;

  const badge = byId("workflowBadge");
  badge.textContent = status.replaceAll("_", " ");

  if (
    status === "PAID" ||
    status === "APPROVED" ||
    status === "AUTO_APPROVED_PENDING_PAYMENT"
  ) {
    badge.className = "badge success";
  } else if (
    status === "REJECTED_BY_HUMAN" ||
    status === "PAYMENT_FAILED"
  ) {
    badge.className = "badge danger";
  } else if (status === "INFO_REQUESTED") {
    badge.className = "badge info";
  } else {
    badge.className = "badge warning";
  }
}

function renderWorkflow() {
  const scenario = currentScenario();

  if (!scenario) {
    byId("workflowEmpty").hidden = false;
    byId("workflowContent").hidden = true;
    return;
  }

  byId("workflowEmpty").hidden = true;
  byId("workflowContent").hidden = false;

  byId("invoiceId").textContent = scenario.invoiceId;
  byId("trackingId").textContent = state.trackingId;
  byId("vendor").textContent = scenario.vendor;
  byId("amount").textContent = formatUSD(scenario.amount);
  byId("revision").textContent = state.revision;
  byId("submissionStatus").textContent = state.status;
  byId("reason").textContent = state.reason;
}

function renderApprovalPanel() {
  const scenario = currentScenario();
  const panel = byId("approvalPanel");

  const visible =
    scenario &&
    (
      state.status === "HUMAN_REVIEW_REQUIRED" ||
      state.status === "PENDING_APPROVAL"
    );

  panel.hidden = !visible;

  if (!visible) {
    return;
  }

  byId("approvalInvoice").textContent = scenario.invoiceId;
  byId("approvalAmount").textContent = formatUSD(scenario.amount);
  byId("approvalRevision").textContent = state.revision;
  byId("agentRecommendation").textContent =
    scenario.recommendation.replaceAll("_", " ");

  byId("violations").textContent =
    scenario.violations.length > 0
      ? scenario.violations.join(", ")
      : "No policy violations";

  byId("approvalBadge").textContent = "Pending review";
  byId("approvalBadge").className = "badge warning";
}

function renderAdditionalInfoPanel() {
  byId("additionalInfoPanel").hidden =
    state.status !== "INFO_REQUESTED";
}

function renderAll() {
  renderWorkflow();
  renderApprovalPanel();
  renderAdditionalInfoPanel();
  renderMetrics();
}

function resetDemo() {
  state.selectedScenario = null;
  state.trackingId = "";
  state.correlationId = "";
  state.status = "";
  state.reason = "";
  state.revision = 0;
  state.events = [];
  state.metrics = {
    total: 0,
    completed: 0,
    pending: 0,
    waiting: 0,
    approvedAmount: 0,
    rejected: 0
  };

  document.querySelectorAll(".scenario-card").forEach(card => {
    card.classList.remove("selected");
  });

  byId("workflowBadge").textContent = "Not started";
  byId("workflowBadge").className = "badge neutral";

  renderAll();
  renderTimeline();
}

function startScenario(name) {
  const scenario = scenarios[name];

  if (!scenario) {
    return;
  }

  state.selectedScenario = name;
  state.trackingId = createIdentifier("sub");
  state.correlationId = createIdentifier("demo");
  state.status = "ACCEPTED";
  state.reason = scenario.initialReason;
  state.revision = 1;
  state.events = [];
  state.metrics.total += 1;

  document.querySelectorAll(".scenario-card").forEach(card => {
    card.classList.toggle(
      "selected",
      card.dataset.scenario === name
    );
  });

  addEvent(
    "Submission accepted",
    scenario.invoiceId + " received by the gateway"
  );

  renderAll();

  byId("advanceButton").textContent = "Run policy evaluation";
}

function advanceWorkflow() {
  const scenario = currentScenario();

  if (!scenario) {
    return;
  }

  if (state.status === "ACCEPTED") {
    setStatus(
      "PROCESSING",
      "The decision service is evaluating deterministic policy."
    );

    addEvent(
      "Policy evaluation started",
      "Agent recommendation recorded; deterministic policy remains authoritative"
    );

    byId("advanceButton").textContent = "Complete decision";
    return;
  }

  if (state.status === "PROCESSING") {
    if (state.selectedScenario === "auto") {
      setStatus(
        "AUTO_APPROVED_PENDING_PAYMENT",
        "The invoice passed deterministic policy and autonomy limits."
      );

      addEvent(
        "Automatically approved",
        "Low-value invoice passed the policy router"
      );

      byId("advanceButton").textContent = "Process payment";
      return;
    }

    setStatus(
      "HUMAN_REVIEW_REQUIRED",
      "Deterministic policy requires a human decision."
    );

    state.metrics.pending += 1;

    addEvent(
      "Human review required",
      scenario.violations.join(", ")
    );

    byId("advanceButton").textContent = "Waiting for approver";
    byId("advanceButton").disabled = true;

    renderAll();
    return;
  }

  if (state.status === "AUTO_APPROVED_PENDING_PAYMENT") {
    setStatus(
      "PAID",
      "Simulated payment completed successfully."
    );

    state.metrics.completed += 1;
    state.metrics.approvedAmount += scenario.amount;

    addEvent(
      "Payment completed",
      "Idempotent simulated payment succeeded"
    );

    byId("advanceButton").textContent = "Workflow complete";
    byId("advanceButton").disabled = true;

    renderAll();
  }
}

function applyApproval(action) {
  const scenario = currentScenario();

  if (!scenario || state.status !== "HUMAN_REVIEW_REQUIRED") {
    return;
  }

  if (action === "request-info") {
    setStatus(
      "INFO_REQUESTED",
      "The approver requested additional information from the submitter."
    );

    state.metrics.pending = Math.max(0, state.metrics.pending - 1);
    state.metrics.waiting += 1;

    addEvent(
      "Additional information requested",
      byId("approvalReason").value ||
        "The submitter must provide more context"
    );

    renderAll();
    return;
  }

  state.metrics.pending = Math.max(0, state.metrics.pending - 1);

  if (action === "reject") {
    state.metrics.completed += 1;
    state.metrics.rejected += 1;

    setStatus(
      "REJECTED_BY_HUMAN",
      byId("approvalReason").value ||
        "The approver rejected the invoice."
    );

    addEvent(
      "Rejected by human",
      state.reason
    );

    byId("advanceButton").disabled = true;
    byId("advanceButton").textContent = "Workflow complete";

    renderAll();
    return;
  }

  setStatus(
    "APPROVED",
    byId("approvalReason").value ||
      "The approver approved the invoice."
  );

  addEvent(
    "Approved by human",
    state.reason
  );

  byId("advanceButton").disabled = true;
  byId("advanceButton").textContent = "Processing payment...";

  renderAll();
}

function submitAdditionalInformation() {
  if (state.status !== "INFO_REQUESTED") {
    return;
  }

  const notes = byId("additionalNotes").value.trim();
  const attendees = Number(byId("additionalAttendees").value || 0);
  const receiptPresent =
    byId("additionalReceipt").value === "true";

  if (!notes && attendees <= 0) {
    window.alert(
      "Provide notes or a positive attendee count before continuing."
    );
    return;
  }

  state.revision += 1;
  state.metrics.waiting = Math.max(0, state.metrics.waiting - 1);

  setStatus(
    "PROCESSING",
    "Additional information received; submission returned for policy evaluation."
  );

  addEvent(
    "Additional information submitted",
    "Revision " +
      state.revision +
      " · attendees " +
      attendees +
      " · receipt " +
      (receiptPresent ? "present" : "not present")
  );

  renderAll();

  window.setTimeout(() => {
    setStatus(
      "HUMAN_REVIEW_REQUIRED",
      "The updated submission still requires human review."
    );

    state.metrics.pending += 1;

    addEvent(
      "Revision returned to human review",
      "Revision " + state.revision + " is now pending approval"
    );

    byId("advanceButton").disabled = true;
    byId("advanceButton").textContent = "Waiting for approver";

    renderAll();
  }, 700);
}

document.querySelectorAll(".scenario-card").forEach(card => {
  card.addEventListener("click", () => {
    byId("advanceButton").disabled = false;
    startScenario(card.dataset.scenario);
  });
});

byId("resetButton").addEventListener("click", resetDemo);
byId("advanceButton").addEventListener("click", advanceWorkflow);

byId("approveButton").addEventListener("click", () => {
  applyApproval("approve");

  if (state.status === "APPROVED") {
    window.setTimeout(() => {
      const scenario = currentScenario();

      setStatus(
        "PAID",
        "Simulated payment completed successfully."
      );

      state.metrics.completed += 1;

      if (scenario) {
        state.metrics.approvedAmount += scenario.amount;
      }

      addEvent(
        "Payment completed",
        "Human-approved invoice was paid idempotently"
      );

      byId("advanceButton").disabled = true;
      byId("advanceButton").textContent = "Workflow complete";

      renderAll();
    }, 700);
  }
});

byId("rejectButton").addEventListener("click", () => {
  applyApproval("reject");
});

byId("requestInfoButton").addEventListener("click", () => {
  applyApproval("request-info");
});

byId("submitAdditionalInfoButton").addEventListener(
  "click",
  submitAdditionalInformation
);

resetDemo();

console.info(
  "ApprovalFlow static portfolio demo loaded. No backend services are running."
);
