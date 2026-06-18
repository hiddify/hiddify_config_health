# Build
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /health-check-server .

# Run
FROM gcr.io/distroless/static-debian12
COPY --from=build /health-check-server /health-check-server
ENV ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/health-check-server"]
