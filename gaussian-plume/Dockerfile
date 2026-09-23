# syntax=docker/dockerfile:1

# ---- 构建阶段：锁定 Go 1.22（具体补丁版，避免在线切换工具链）----
FROM golang:1.22.12-bookworm AS build
WORKDIR /src
ENV GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux

# 先装依赖，利用层缓存
COPY go.mod go.sum ./
RUN go mod download

# 再拷源码构建（CGO 关闭，得到静态二进制）
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/plume-server ./cmd/server

# ---- 运行阶段 ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/plume-server /plume-server
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/plume-server"]
