# syntax=docker/dockerfile:1
# Multi-stage build producing both binaries (api + batch) in one image.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api    ./cmd/api  \
 && CGO_ENABLED=0 go build -o /out/batch  ./cmd/batch \
 && CGO_ENABLED=0 go build -o /out/oauth  ./cmd/oauth

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/api   /app/api
COPY --from=build /out/batch /app/batch
COPY --from=build /out/oauth /app/oauth
# Default to the API server; the batch Job overrides the command.
# The ADK web launcher needs its sublaunchers (api/a2a) listed explicitly,
# even in prod (ADK_LAUNCHER=prod). CMD supplies them as default args so the
# image runs standalone; k8s may still override args if needed.
ENV ADK_LAUNCHER=prod
ENTRYPOINT ["/app/api"]
CMD ["web", "api", "a2a"]
