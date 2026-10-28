FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY simulator.py .
ENTRYPOINT ["python", "-u", "simulator.py"]
CMD ["--url", "ws://ingest:8080/ws", "--vehicles", "50", "--interval", "1"]
