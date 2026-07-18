# Runtime stage packages binaries and Wiki assets prepared by the release workflow.
FROM ubuntu:24.04

ARG TARGETARCH=amd64

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y ca-certificates git wget \
    && groupadd --system ccg \
    && useradd --system --gid ccg --create-home --home-dir /home/ccg ccg \
    && mkdir -p /repo /repos /home/ccg/.cache /home/ccg/.config/ccg \
    && chown -R ccg:ccg /repo /repos /home/ccg \
    && rm -rf /var/lib/apt/lists/*

COPY container-artifacts/${TARGETARCH}/ccg /usr/local/bin/ccg
COPY container-artifacts/${TARGETARCH}/ccg-server /usr/local/bin/ccg-server
COPY container-artifacts/wiki /usr/share/ccg/wiki

WORKDIR /repo
USER ccg
ENV HOME=/home/ccg \
    XDG_CACHE_HOME=/home/ccg/.cache \
    CCG_REPO_ROOT=/repos
EXPOSE 8080

ENTRYPOINT ["ccg-server"]
CMD ["--http-addr", ":8080", "--wiki-dir", "/usr/share/ccg/wiki"]
