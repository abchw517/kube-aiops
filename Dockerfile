FROM golang:1.24.6-alpine3.22 AS builder

WORKDIR /src

COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags='-s -w' \
    -o /out/kube-aiops-api \
    ./cmd/api

FROM scratch

USER 65532:65532

COPY --from=builder /out/kube-aiops-api /kube-aiops-api

EXPOSE 8080

ENTRYPOINT ["/kube-aiops-api"]
