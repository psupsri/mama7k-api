# Production image of the API. The web app is a separate repo, deployed as a static site
# that forwards /api/* here (see docs/deploy.md).

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# distroless/static has CA certificates (for Neon and R2 over TLS) and runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /app/server
EXPOSE 8787
ENTRYPOINT ["/app/server"]
