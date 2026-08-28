FROM golang:1.26-alpine AS builder
ARG VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_DATE=unknown
WORKDIR /app
RUN apk add --no-cache build-base
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build \
    -trimpath \
    -ldflags="-s -w \
      -X 'github.com/Team-Haruki/Haruki-Toolbox-Backend/version.Version=${VERSION}' \
      -X 'github.com/Team-Haruki/Haruki-Toolbox-Backend/version.Commit=${GIT_SHA}' \
      -X 'github.com/Team-Haruki/Haruki-Toolbox-Backend/version.BuildDate=${BUILD_DATE}'" \
    -o haruki-toolbox-backend ./main.go

FROM alpine:3.24

ENV TZ=Asia/Shanghai
ARG VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$GIT_SHA \
      org.opencontainers.image.created=$BUILD_DATE

WORKDIR /app
# The uid/gid are PINNED. `adduser -S` picks the first free system id, which a
# base-image change can shift — and the deployment bind-mounts its config, logs
# and avatar directory from the host, where ownership is enforced by number. A
# drifting uid would turn into an unreadable config and a crash loop, so the
# host side is chowned to exactly these ids.
RUN apk --no-cache add ca-certificates tzdata \
    && addgroup -g 10001 -S haruki \
    && adduser -u 10001 -S -G haruki haruki \
    && mkdir -p logs \
    && chown haruki:haruki logs

COPY --from=builder --chown=haruki:haruki /app/haruki-toolbox-backend .

EXPOSE 6666
USER haruki
ENTRYPOINT ["./haruki-toolbox-backend"]
