FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS builder
WORKDIR /app
# Modules in their own layer: it only changes with go.mod/go.sum, and CI keeps it in the
# registry build cache.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Build args are declared here, right before the build, not at the top: an ARG becomes part of
# the environment of every later RUN, so per-commit values (GIT_SHA, BUILD_DATE) used to re-run
# every layer. Cross-compiles for the requested platform (TARGETOS/TARGETARCH) from the native
# builder; CGO is off, so no C toolchain is needed.
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath \
    -ldflags="-s -w \
      -X 'github.com/Team-Haruki/Haruki-Toolbox-Backend/version.Version=${VERSION}' \
      -X 'github.com/Team-Haruki/Haruki-Toolbox-Backend/version.Commit=${GIT_SHA}' \
      -X 'github.com/Team-Haruki/Haruki-Toolbox-Backend/version.BuildDate=${BUILD_DATE}'" \
    -o haruki-toolbox-backend ./main.go

FROM alpine:3.24

ENV TZ=Asia/Shanghai

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

# After the RUN above, so the per-commit values do not re-run it.
ARG VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$GIT_SHA \
      org.opencontainers.image.created=$BUILD_DATE

COPY --from=builder --chown=haruki:haruki /app/haruki-toolbox-backend .
COPY --from=builder --chown=haruki:haruki /app/data ./data

EXPOSE 6666
USER haruki
ENTRYPOINT ["./haruki-toolbox-backend"]
