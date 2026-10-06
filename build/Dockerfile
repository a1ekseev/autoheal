FROM python:3.10-slim

WORKDIR /app
COPY /app .

RUN pip install poetry && poetry config virtualenvs.create false \
    && poetry install --no-root

ENTRYPOINT ["python", "-m", "monitor"]