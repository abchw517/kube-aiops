FROM mharbor-cs.cloud.kemai.cn/public/golang:1.26.5-alpine3.23 AS builder

WORKDIR /src

ARG GOPROXY=https://goproxy.cn
ENV GOPROXY=${GOPROXY} \
    GOSUMDB=sum.golang.org \
    CGO_ENABLED=0 \
    GOOS=linux

COPY go.mod go.sum ./
# 下载依赖，并持久化 Go Modules 缓存
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    go mod download

COPY cmd ./cmd
COPY internal ./internal

# 缓存模块和 Go 编译产物
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go build \
      -trimpath \
      -ldflags='-s -w' \
      -o /out/kube-aiops-api \
      ./cmd/api

FROM scratch

USER 65532:65532

COPY --from=builder /out/kube-aiops-api /kube-aiops-api
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

EXPOSE 8080

ENTRYPOINT ["/kube-aiops-api"]
