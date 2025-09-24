FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN apk add --no-cache build-base
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -ldflags="-s -w -X=haruki-suite/version.Version=${VERSION}" -o haruki-toolbox-backend ./main.go

FROM scratch
WORKDIR /app
COPY --from=builder /app/haruki-toolbox-backend .
EXPOSE 6666
ENTRYPOINT ["./haruki-toolbox-backend"]