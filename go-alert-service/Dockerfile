FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy go module files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy all source code
COPY . .

# Build the application (pure Go — no CGO needed for Postgres)
RUN CGO_ENABLED=0 GOOS=linux go build -o alert-service .

# Minimal final image (pinned for reproducibility — OWASP A06)
FROM alpine:3.20

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/alert-service .

# Expose the port the service will run on
EXPOSE 8081

# Run the service
CMD ["./alert-service"]
