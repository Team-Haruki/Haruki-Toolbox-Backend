FROM golang:1.26-alpine AS builder
ARG VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_DATE=unknown
WORKDIR /app
COPY . .
RUN apk add --no-cache build-base
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build \
    -trimpath \
    -ldflags="-s -w \
      -X 'haruki-suite/version.Version=${VERSION}' \
      -X 'haruki-suite/version.Commit=${GIT_SHA}' \
      -X 'haruki-suite/version.BuildDate=${BUILD_DATE}'" \
    -o haruki-toolbox-backend ./main.go

FROM alpine:3.20

ENV TZ=Asia/Shanghai
ARG VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$GIT_SHA \
      org.opencontainers.image.created=$BUILD_DATE

WORKDIR /app
RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /app/haruki-toolbox-backend .
RUN mkdir -p logs

EXPOSE 6666
ENTRYPOINT ["./haruki-toolbox-backend"]