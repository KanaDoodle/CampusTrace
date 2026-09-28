ARG GO_IMAGE=golang:1.25.9-bookworm
FROM ${GO_IMAGE} AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
COPY migrations ./migrations
RUN go build -trimpath -o /out/api ./cmd/api && \
    go build -trimpath -o /out/worker ./cmd/worker && \
    go build -trimpath -o /out/analysis ./cmd/analysis && \
    go build -trimpath -o /out/campustrace ./cmd/campustrace

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/ /app/
USER 65532:65532
WORKDIR /app
CMD ["/app/api"]
