FROM alpine:3.22 AS download
RUN apk add --no-cache ca-certificates python3
COPY deploy/download_release.py /download_release.py
ARG RELEASE_VERSION=latest
ARG TARGETARCH
RUN python3 /download_release.py "$RELEASE_VERSION" "$TARGETARCH" /release

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S webui && adduser -S -G webui webui \
    && mkdir /data && chown webui:webui /data
COPY --from=download /release/webui /usr/local/bin/webui
COPY --from=download /release/lingosbot /release/worker-release.json /usr/local/share/lingosbot/
# Older published WebUIs do not yet manage workers; keep their default path usable.
RUN ln -s /usr/local/share/lingosbot/lingosbot /usr/local/bin/lingosbot
VOLUME /data
WORKDIR /data
USER webui
ARG RELEASE_VERSION=latest
ENV DB_PATH=/data/webui.db LISTEN=:8080 COOKIE_SECURE=1 RELEASE_VERSION=$RELEASE_VERSION
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/webui"]
