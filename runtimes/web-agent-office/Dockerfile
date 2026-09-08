FROM python:3.12-slim-bookworm
RUN --mount=type=cache,id=webagent-office-apt,target=/var/cache/apt,sharing=locked \
    sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources \
    && rm -f /etc/apt/apt.conf.d/docker-clean \
    && apt-get -o Acquire::Retries=3 update \
    && apt-get -o Acquire::Retries=3 install -y --no-install-recommends libreoffice-impress libreoffice-writer libreoffice-calc fonts-noto-cjk fonts-dejavu-core \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY schema.py renderers.py server.py ./
RUN useradd --uid 10001 --create-home renderer
USER 10001:10001
ENV HOST=0.0.0.0 PORT=8090 OFFICE_BINARY=/usr/bin/libreoffice PYTHONDONTWRITEBYTECODE=1
EXPOSE 8090
CMD ["python", "server.py"]
