FROM python:3.13-slim AS builder

RUN pip install --no-cache-dir uv

WORKDIR /build
COPY pyproject.toml uv.lock ./
COPY src/ src/

RUN uv sync --frozen --no-dev

FROM python:3.13-slim

RUN adduser --disabled-password --uid 1000 bridge

WORKDIR /app
COPY --from=builder /build/.venv /app/.venv
COPY --from=builder /build/src /app/src

ENV PATH="/app/.venv/bin:$PATH"
ENV BRIDGE_URL="http://bridge:8080"

USER bridge

EXPOSE 3000

ENTRYPOINT ["python", "-m", "whatsapp_mcp.main"]
