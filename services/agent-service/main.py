from datetime import datetime, timezone

from fastapi import FastAPI

app = FastAPI(title="ApprovalFlow Agent Service")


@app.get("/healthz")
def healthz():
    return {
        "service": "agent-service",
        "status": "ok",
        "time_utc": datetime.now(timezone.utc).isoformat(),
    }
