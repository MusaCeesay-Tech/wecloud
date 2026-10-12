# syntax=docker/dockerfile:1
FROM python:3.11-alpine
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY worker.py .
EXPOSE 5001
CMD ["python", "worker.py"]