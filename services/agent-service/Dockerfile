FROM python:3.12-slim

WORKDIR /app

COPY services/agent-service/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

COPY services/agent-service/main.py /app/main.py
COPY data/policy.md /app/data/policy.md

EXPOSE 8080

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080"]
