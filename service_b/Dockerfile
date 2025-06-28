FROM golang:latest AS builder
WORKDIR /app
COPY . .
RUN GOOS=linux CGO_ENABLED=0 go build -o server service_b/main.go

FROM alpine
COPY --from=builder /app/server .
CMD ["./server"]