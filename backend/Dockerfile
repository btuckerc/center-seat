FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/centerseat-api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/centerseat-api /centerseat-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/centerseat-api"]
