FROM python:3.12-slim AS base

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1

WORKDIR /app
COPY requirements.txt pymupdf4llm-wheel.sha256 ./
RUN python -m pip download --no-deps pymupdf4llm==1.28.2 \
    && sha256sum -c pymupdf4llm-wheel.sha256 \
    && python -m pip install -r requirements.txt \
    && rm pymupdf4llm-1.28.2-py3-none-any.whl

COPY source_map_parser ./source_map_parser

FROM base AS test
COPY requirements-test.txt ./
RUN python -m pip install -r requirements-test.txt
COPY tests ./tests
CMD ["python", "-m", "unittest", "discover", "-s", "/app/tests", "-v"]

FROM base AS runtime
USER 65532:65532
EXPOSE 8086
CMD ["uvicorn", "source_map_parser.service:app", "--host", "0.0.0.0", "--port", "8086", "--workers", "1", "--no-access-log"]
