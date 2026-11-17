ARG GO_IMAGE=public.ecr.aws/docker/library/golang:1.27.1-bookworm@sha256:8d48e12ec56735e9358640898b9d9b9fcca110612ed8a5567438c0a1baa24e66
ARG MYSQL_IMAGE=public.ecr.aws/docker/library/mysql:8.4@sha256:6ea90827b1100f8f2ae306a539f86d2c264a26ed435a2a9f75551dd5c3aeb242

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY config/ config/
COPY database/ database/
COPY handlers/ handlers/
COPY installer/ installer/
COPY internal/ internal/
COPY mailer/ mailer/
COPY middleware/ middleware/
COPY models/ models/
COPY routes/ routes/
COPY storage/ storage/
COPY utils/ utils/
COPY main.go healthcheck.go ./
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false -tags netgo,osusergo -ldflags='-s -w' -o /out/zhuapic .

FROM ${MYSQL_IMAGE} AS client
COPY docker/collect-runtime.sh /collect-runtime.sh
RUN /bin/bash /collect-runtime.sh

FROM scratch
LABEL org.opencontainers.image.title="Origin" \
      org.opencontainers.image.version="0.6.0" \
      org.opencontainers.image.description="Origin self-hosted image hosting (Go + MySQL)" \
      org.opencontainers.image.source="https://github.com/SileyOf/orimg" \
      org.opencontainers.image.licenses="AGPL-3.0-only"
COPY --from=client /runtime/ /
COPY --from=build /out/zhuapic /app/zhuapic
COPY static/ /app/static/
WORKDIR /app
# Data paths use the application defaults (data/... relative to /app), which
# resolve to the /app/data volume. They are deliberately not set here so that a
# mounted legacy .env can still override them exactly as on a binary install.
ENV PORT=8080 PATH=/usr/bin:/app TZ=Asia/Shanghai
USER 10001:10001
VOLUME ["/app/data"]
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD ["/app/zhuapic", "-healthcheck"]
ENTRYPOINT ["/app/zhuapic"]
